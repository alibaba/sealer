// Copyright © 2023 Alibaba Group Holding Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cluster

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/sealerio/sealer/cmd/sealer/cmd/types"
	"github.com/sealerio/sealer/cmd/sealer/cmd/utils"
	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/application"
	clusterruntime "github.com/sealerio/sealer/pkg/cluster-runtime"
	"github.com/sealerio/sealer/pkg/clusterfile"
	containerruntime "github.com/sealerio/sealer/pkg/container-runtime"
	imagev1 "github.com/sealerio/sealer/pkg/define/image/v1"
	"github.com/sealerio/sealer/pkg/imagedistributor"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/sealerio/sealer/pkg/infradriver"
	"github.com/sealerio/sealer/pkg/registry"
	v1 "github.com/sealerio/sealer/types/api/v1"
	v2 "github.com/sealerio/sealer/types/api/v2"
	"github.com/sealerio/sealer/utils/os/fs"
	"github.com/sealerio/sealer/utils/platform"
	"github.com/sirupsen/logrus"
)

type AppInstaller struct {
	cf             clusterfile.Interface
	imageExtension imagev1.ImageExtension
	infraDriver    infradriver.InfraDriver
	appDriver      application.Interface
	imageEngine    imageengine.Interface
}

type AppInstallOptions struct {
	Envs                    []string
	RunMode                 string
	SkipPrepareAppMaterials bool
	IgnoreCache             bool
	Distribution            types.DistributionMethod
}

func (i AppInstaller) Install(imageName string, options AppInstallOptions) error {
	logrus.Infof("start to install application using image: %s", imageName)

	i.infraDriver.AddClusterEnv(options.Envs)

	if !options.SkipPrepareAppMaterials {
		if err := i.prepareMaterials(imageName, options.RunMode, options.IgnoreCache, options.Distribution); err != nil {
			return err
		}
	}
	if options.RunMode == common.ApplyModeLoadImage {
		return nil
	}

	if err := i.appDriver.Launch(i.infraDriver); err != nil {
		return err
	}
	if err := i.appDriver.Save(application.SaveOptions{}); err != nil {
		return err
	}

	//save and commit
	i.cf.SetApplication(i.appDriver.GetApplication())
	confPath := clusterruntime.GetClusterConfPath(i.imageExtension.Labels)
	if err := i.cf.SaveAll(clusterfile.SaveOptions{CommitToCluster: false, ConfPath: confPath}); err != nil {
		return err
	}

	logrus.Infof("succeeded in installing application with image %s", imageName)

	return nil
}

func (i AppInstaller) prepareMaterials(appImageName string, mode string, ignoreCache bool, distribution types.DistributionMethod) error {
	clusterHosts := i.infraDriver.GetHostIPList()
	clusterHostsPlatform, err := i.infraDriver.GetHostsPlatform(clusterHosts)
	if err != nil {
		return err
	}

	imageMounter, err := imagedistributor.NewImageMounter(i.imageEngine, clusterHostsPlatform)
	if err != nil {
		return err
	}

	imageMountInfo, err := imageMounter.Mount(appImageName)
	if err != nil {
		return err
	}

	defer func() {
		err = imageMounter.Umount(appImageName, imageMountInfo)
		if err != nil {
			logrus.Errorf("failed to umount sealer image: %v", err)
		}
	}()

	for _, info := range imageMountInfo {
		err = i.appDriver.FileProcess(info.MountDir)
		if err != nil {
			return errors.Wrapf(err, "failed to execute file processor")
		}
	}

	var distributor imagedistributor.Distributor
	if distribution == types.P2PDistribution {
		distributor, err = imagedistributor.NewP2PDistributor(imageMountInfo, i.infraDriver, nil, imagedistributor.DistributeOption{
			IgnoreCache: ignoreCache,
		})
		if err != nil {
			logrus.Warnf("failed to initialize P2P-based distributor: %s", err)
		}
	} else {
		distributor, err = imagedistributor.NewScpDistributor(imageMountInfo, i.infraDriver, nil, imagedistributor.DistributeOption{
			IgnoreCache: ignoreCache,
		})
		if err != nil {
			return err
		}
	}

	if mode == common.ApplyModeLoadImage {
		return loadToRegistry(i.infraDriver, distributor)
	}

	masters := i.infraDriver.GetHostIPListByRole(common.MASTER)
	regConfig := i.infraDriver.GetClusterRegistry()
	// distribute rootfs

	if err := distributor.Distribute(masters, i.infraDriver.GetClusterRootfsPath()); err != nil {
		return err
	}

	//if we use local registry service, load container image to registry
	if regConfig.LocalRegistry == nil {
		return nil
	}
	deployHosts := masters
	if !*regConfig.LocalRegistry.HA {
		deployHosts = []net.IP{masters[0]}
	}

	registryConfigurator, err := registry.NewConfigurator(deployHosts,
		containerruntime.Info{},
		regConfig, i.infraDriver, distributor)
	if err != nil {
		return err
	}

	registryDriver, err := registryConfigurator.GetDriver()
	if err != nil {
		return err
	}

	err = registryDriver.UploadContainerImages2Registry()
	if err != nil {
		return err
	}

	return nil
}

func NewApplicationInstaller(appSpec *v2.Application, extension imagev1.ImageExtension, imageEngine imageengine.Interface) (*AppInstaller, error) {
	v2App, err := application.NewAppDriver(appSpec, extension)
	if err != nil {
		return nil, fmt.Errorf("failed to parse application:%v ", err)
	}

	cf, _, err := clusterfile.GetActualClusterFile()
	if err != nil {
		return nil, err
	}

	cluster := cf.GetCluster()
	infraDriver, err := infradriver.NewInfraDriver(&cluster)
	if err != nil {
		return nil, err
	}

	return &AppInstaller{
		cf:             cf,
		imageExtension: extension,
		appDriver:      v2App,
		infraDriver:    infraDriver,
		imageEngine:    imageEngine,
	}, nil
}

type KubeInstaller struct {
	cf          clusterfile.Interface
	infraDriver infradriver.InfraDriver
	imageEngine imageengine.Interface
	imageSpec   *imagev1.ImageSpec
}

type KubeInstallOptions struct {
	RunMode         string
	IgnoreCache     bool
	P2PDistribution bool
}

type KubeScaleUpOptions struct {
	IgnoreCache bool
}

type KubeScaleDownOptions struct {
	Prune bool
}

type KubeDeleteOptions struct {
	Prune bool
}

func (k KubeInstaller) Install(kubeImageName string, options KubeInstallOptions) error {
	var (
		// cluster parameters
		cluster      = k.cf.GetCluster()
		clusterHosts = k.infraDriver.GetHostIPList()

		pluginsFromFile       = k.cf.GetPlugins()
		configsFromFile       = k.cf.GetConfigs()
		kubeadmConfigFromFile = k.cf.GetKubeadmConfig()

		// app parameters
		cmds     = k.infraDriver.GetClusterLaunchCmds()
		appNames = k.infraDriver.GetClusterLaunchApps()
	)

	logrus.Infof("start to create new cluster with image: %s", kubeImageName)
	logrus.Debugf("will create a new cluster using: %+v\n", cluster)

	clusterHostsPlatform, err := k.infraDriver.GetHostsPlatform(clusterHosts)
	if err != nil {
		return err
	}

	imageMounter, err := imagedistributor.NewImageMounter(k.imageEngine, clusterHostsPlatform)
	if err != nil {
		return err
	}

	imageMountInfo, err := imageMounter.Mount(kubeImageName)
	if err != nil {
		return err
	}

	defer func() {
		err = imageMounter.Umount(kubeImageName, imageMountInfo)
		if err != nil {
			logrus.Errorf("failed to umount sealer image")
		}
	}()

	// new merge image extension with app
	v2App, err := application.NewAppDriver(utils.ConstructApplication(k.cf.GetApplication(), cmds, appNames, cluster.Spec.Env), k.imageSpec.ImageExtension)
	if err != nil {
		return fmt.Errorf("failed to parse application from Clusterfile:%v ", err)
	}

	// process app files
	for _, info := range imageMountInfo {
		err = v2App.FileProcess(info.MountDir)
		if err != nil {
			return errors.Wrapf(err, "failed to execute file processor")
		}
	}

	var distributor imagedistributor.Distributor
	if options.P2PDistribution {
		distributor, err = imagedistributor.NewP2PDistributor(imageMountInfo, k.infraDriver, configsFromFile, imagedistributor.DistributeOption{
			IgnoreCache: options.IgnoreCache,
		})
		if err != nil {
			logrus.Warnf("failed to initialize P2P-based distributor: %s", err)
		}
	} else {
		distributor, err = imagedistributor.NewScpDistributor(imageMountInfo, k.infraDriver, configsFromFile, imagedistributor.DistributeOption{
			IgnoreCache: options.IgnoreCache,
		})
		if err != nil {
			return err
		}
	}

	if options.RunMode == common.ApplyModeLoadImage {
		return clusterruntime.LoadToRegistry(k.infraDriver, distributor)
	}

	plugins, err := loadPluginsFromImage(imageMountInfo)
	if err != nil {
		return err
	}

	if pluginsFromFile != nil {
		plugins = append(plugins, pluginsFromFile...)
	}

	runtimeConfig := &clusterruntime.RuntimeConfig{
		Distributor:            distributor,
		Plugins:                plugins,
		ContainerRuntimeConfig: cluster.Spec.ContainerRuntime,
	}

	if kubeadmConfigFromFile != nil {
		runtimeConfig.KubeadmConfig = *kubeadmConfigFromFile
	}

	installer, err := clusterruntime.NewInstaller(k.infraDriver, *runtimeConfig, clusterruntime.GetClusterInstallInfo(k.imageSpec.ImageExtension.Labels, cluster.Spec.ContainerRuntime))
	if err != nil {
		return err
	}

	//we need to save desired clusterfile to local disk temporarily
	//and will use it later to clean the cluster node if apply failed.
	if err = k.cf.SaveAll(clusterfile.SaveOptions{}); err != nil {
		return err
	}

	// install cluster
	err = installer.Install()
	if err != nil {
		return err
	}

	// install application
	if err = v2App.Launch(k.infraDriver); err != nil {
		return err
	}
	if err = v2App.Save(application.SaveOptions{}); err != nil {
		return err
	}

	//save and commit
	confPath := clusterruntime.GetClusterConfPath(k.imageSpec.ImageExtension.Labels)
	if err = k.cf.SaveAll(clusterfile.SaveOptions{CommitToCluster: false, ConfPath: confPath}); err != nil {
		return err
	}

	logrus.Infof("succeeded in creating new cluster with image %s", kubeImageName)

	return nil
}

func (k KubeInstaller) ScaleUp(scaleUpMasterIPList, scaleUpNodeIPList []net.IP, options KubeScaleUpOptions) error {
	logrus.Infof("start to scale up cluster")

	var (
		newHosts              = append(scaleUpMasterIPList, scaleUpNodeIPList...)
		clusterImageName      = k.infraDriver.GetClusterImageName()
		cluster               = k.cf.GetCluster()
		pluginsFromFile       = k.cf.GetPlugins()
		configsFromFile       = k.cf.GetConfigs()
		kubeadmConfigFromFile = k.cf.GetKubeadmConfig()
	)

	clusterHostsPlatform, err := k.infraDriver.GetHostsPlatform(newHosts)
	if err != nil {
		return err
	}

	imageMounter, err := imagedistributor.NewImageMounter(k.imageEngine, clusterHostsPlatform)
	if err != nil {
		return err
	}

	imageMountInfo, err := imageMounter.Mount(clusterImageName)
	if err != nil {
		return err
	}
	defer func() {
		err = imageMounter.Umount(clusterImageName, imageMountInfo)
		if err != nil {
			logrus.Errorf("failed to umount sealer image")
		}
	}()

	distributor, err := imagedistributor.NewScpDistributor(imageMountInfo, k.infraDriver, configsFromFile, imagedistributor.DistributeOption{
		IgnoreCache: options.IgnoreCache,
	})
	if err != nil {
		return err
	}

	plugins, err := loadPluginsFromImage(imageMountInfo)
	if err != nil {
		return err
	}

	if pluginsFromFile != nil {
		plugins = append(plugins, pluginsFromFile...)
	}

	runtimeConfig := &clusterruntime.RuntimeConfig{
		Distributor:            distributor,
		Plugins:                plugins,
		ContainerRuntimeConfig: cluster.Spec.ContainerRuntime,
	}

	if kubeadmConfigFromFile != nil {
		runtimeConfig.KubeadmConfig = *kubeadmConfigFromFile
	}

	installer, err := clusterruntime.NewInstaller(k.infraDriver, *runtimeConfig,
		clusterruntime.GetClusterInstallInfo(k.imageSpec.ImageExtension.Labels, runtimeConfig.ContainerRuntimeConfig))
	if err != nil {
		return err
	}

	//we need to save desired clusterfile to local disk temporarily.
	//and will use it later to clean the cluster node if ScaleUp failed.
	if err = k.cf.SaveAll(clusterfile.SaveOptions{}); err != nil {
		return err
	}

	_, _, err = installer.ScaleUp(scaleUpMasterIPList, scaleUpNodeIPList)
	if err != nil {
		return err
	}

	confPath := clusterruntime.GetClusterConfPath(k.imageSpec.ImageExtension.Labels)
	if err = k.cf.SaveAll(clusterfile.SaveOptions{CommitToCluster: false, ConfPath: confPath}); err != nil {
		return err
	}

	logrus.Infof("succeeded in scaling up cluster")

	return nil
}

func (k KubeInstaller) ScaleDown(deleteMasterIPList, deleteNodeIPList []net.IP, options KubeScaleDownOptions) error {
	logrus.Infof("start to scale down cluster")

	var (
		newHosts              = append(deleteMasterIPList, deleteMasterIPList...)
		clusterImageName      = k.infraDriver.GetClusterImageName()
		cluster               = k.cf.GetCluster()
		pluginsFromFile       = k.cf.GetPlugins()
		kubeadmConfigFromFile = k.cf.GetKubeadmConfig()
		runtimeConfig         = &clusterruntime.RuntimeConfig{
			ContainerRuntimeConfig: cluster.Spec.ContainerRuntime,
		}
	)

	clusterHostsPlatform, err := k.infraDriver.GetHostsPlatform(newHosts)
	if err != nil {
		logrus.Warn("failed to get hosts platform for deleting node, we will skip reset work on it in next steps")
	} else {
		imageMounter, err := imagedistributor.NewImageMounter(k.imageEngine, clusterHostsPlatform)
		if err != nil {
			return err
		}

		imageMountInfo, err := imageMounter.Mount(clusterImageName)
		if err != nil {
			return err
		}
		defer func() {
			err = imageMounter.Umount(clusterImageName, imageMountInfo)
			if err != nil {
				logrus.Errorf("failed to umount sealer image: %v", err)
			}
		}()

		distributor, err := imagedistributor.NewScpDistributor(imageMountInfo, k.infraDriver, nil, imagedistributor.DistributeOption{
			Prune: options.Prune,
		})
		if err != nil {
			return err
		}
		runtimeConfig.Distributor = distributor

		plugins, err := loadPluginsFromImage(imageMountInfo)
		if err != nil {
			return err
		}

		if pluginsFromFile != nil {
			plugins = append(plugins, pluginsFromFile...)
		}
		runtimeConfig.Plugins = plugins
	}

	if kubeadmConfigFromFile != nil {
		runtimeConfig.KubeadmConfig = *kubeadmConfigFromFile
	}

	installer, err := clusterruntime.NewInstaller(k.infraDriver, *runtimeConfig,
		clusterruntime.GetClusterInstallInfo(k.imageSpec.ImageExtension.Labels, cluster.Spec.ContainerRuntime))
	if err != nil {
		return err
	}

	_, _, err = installer.ScaleDown(deleteMasterIPList, deleteNodeIPList)
	if err != nil {
		return err
	}

	if err = utils.ConstructClusterForScaleDown(&cluster, deleteMasterIPList, deleteNodeIPList); err != nil {
		return err
	}
	k.cf.SetCluster(cluster)

	confPath := clusterruntime.GetClusterConfPath(k.imageSpec.ImageExtension.Labels)
	if err = k.cf.SaveAll(clusterfile.SaveOptions{CommitToCluster: false, ConfPath: confPath}); err != nil {
		return err
	}

	return nil
}

func (k KubeInstaller) Delete(options KubeDeleteOptions) error {
	logrus.Infof("start to delete cluster")

	var (
		clusterImageName      = k.infraDriver.GetClusterImageName()
		cluster               = k.cf.GetCluster()
		pluginsFromFile       = k.cf.GetPlugins()
		kubeadmConfigFromFile = k.cf.GetKubeadmConfig()
	)

	clusterHostsPlatform, err := k.infraDriver.GetHostsPlatform(k.infraDriver.GetHostIPList())
	if err != nil {
		return err
	}

	imageMounter, err := imagedistributor.NewImageMounter(k.imageEngine, clusterHostsPlatform)
	if err != nil {
		return err
	}

	imageMountInfo, err := imageMounter.Mount(clusterImageName)
	if err != nil {
		return err
	}
	defer func() {
		err = imageMounter.Umount(clusterImageName, imageMountInfo)
		if err != nil {
			logrus.Errorf("failed to umount sealer image: %v", err)
		}
	}()

	distributor, err := imagedistributor.NewScpDistributor(imageMountInfo, k.infraDriver, nil, imagedistributor.DistributeOption{
		Prune: options.Prune,
	})
	if err != nil {
		return err
	}

	plugins, err := loadPluginsFromImage(imageMountInfo)
	if err != nil {
		return err
	}

	if pluginsFromFile != nil {
		plugins = append(plugins, pluginsFromFile...)
	}

	runtimeConfig := &clusterruntime.RuntimeConfig{
		Distributor:            distributor,
		Plugins:                plugins,
		ContainerRuntimeConfig: cluster.Spec.ContainerRuntime,
	}

	if kubeadmConfigFromFile != nil {
		runtimeConfig.KubeadmConfig = *kubeadmConfigFromFile
	}

	installer, err := clusterruntime.NewInstaller(k.infraDriver, *runtimeConfig,
		clusterruntime.GetClusterInstallInfo(k.imageSpec.ImageExtension.Labels, cluster.Spec.ContainerRuntime))
	if err != nil {
		return err
	}

	if err = installer.UnInstall(); err != nil {
		return err
	}
	//delete local files,including clusterfile, application.json under sealer work dir
	if err = os.Remove(common.GetDefaultClusterfile()); err != nil {
		return err
	}

	if err = os.Remove(common.GetDefaultApplicationFile()); err != nil {
		return err
	}

	//delete kubeconfig under home dir.
	if err = fs.FS.RemoveAll(common.DefaultKubeConfigDir()); err != nil {
		return err
	}

	return nil
}

func NewKubeInstaller(cf clusterfile.Interface, imageEngine imageengine.Interface, imageSpec *imagev1.ImageSpec) (*KubeInstaller, error) {
	cluster := cf.GetCluster()

	// merge image extension with cluster
	mergedWithExt := utils.MergeClusterWithImageExtension(&cluster, imageSpec.ImageExtension)

	cf.SetCluster(*mergedWithExt)

	infraDriver, err := infradriver.NewInfraDriver(mergedWithExt)
	if err != nil {
		return nil, err
	}

	return &KubeInstaller{
		imageEngine: imageEngine,
		imageSpec:   imageSpec,
		infraDriver: infraDriver,
		cf:          cf,
	}, nil
}

func loadPluginsFromImage(imageMountInfo []imagedistributor.ClusterImageMountInfo) (plugins []v1.Plugin, err error) {
	for _, info := range imageMountInfo {
		defaultPlatform := platform.GetDefaultPlatform()
		if info.Platform.ToString() == defaultPlatform.ToString() {
			plugins, err = clusterruntime.LoadPluginsFromFile(filepath.Join(info.MountDir, "plugins"))
			if err != nil {
				return
			}
		}
	}

	return plugins, nil
}

// loadToRegistry just load container image to local registry
func loadToRegistry(infraDriver infradriver.InfraDriver, distributor imagedistributor.Distributor) error {
	regConfig := infraDriver.GetClusterRegistry()
	// todo only support load image to local registry at present
	if regConfig.LocalRegistry == nil {
		return nil
	}

	deployHosts := infraDriver.GetHostIPListByRole(common.MASTER)
	if len(deployHosts) < 1 {
		return fmt.Errorf("local registry host can not be nil")
	}
	master0 := deployHosts[0]

	logrus.Infof("start to apply with mode(%s)", common.ApplyModeLoadImage)
	if !*regConfig.LocalRegistry.HA {
		deployHosts = []net.IP{master0}
	}

	if err := distributor.DistributeRegistry(deployHosts, filepath.Join(infraDriver.GetClusterRootfsPath(), "registry")); err != nil {
		return err
	}

	logrus.Infof("load image success")
	return nil
}
