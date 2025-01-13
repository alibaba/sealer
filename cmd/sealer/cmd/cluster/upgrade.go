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
	"os"
	"path/filepath"

	"github.com/sealerio/sealer/cmd/sealer/cmd/types"
	"github.com/sealerio/sealer/cmd/sealer/cmd/utils"
	"github.com/sealerio/sealer/pkg/application"
	clusterruntime "github.com/sealerio/sealer/pkg/cluster-runtime"
	"github.com/sealerio/sealer/pkg/clusterfile"
	imagev1 "github.com/sealerio/sealer/pkg/define/image/v1"
	"github.com/sealerio/sealer/pkg/define/options"
	"github.com/sealerio/sealer/pkg/imagedistributor"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/sealerio/sealer/pkg/infradriver"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

var (
	exampleForUpgradeCmd = `
  sealer upgrade -f upgrade.yaml
`
	longDescriptionForUpgradeCmd = `upgrade command is used to upgrade a Kubernetes cluster via specified Clusterfile.`
)

func NewUpgradeCmd() *cobra.Command {
	upgradeFlags := &types.UpgradeFlags{}
	upgradeCmd := &cobra.Command{
		Use:     "upgrade",
		Short:   "upgrade a Kubernetes cluster via specified Clusterfile",
		Long:    longDescriptionForUpgradeCmd,
		Example: exampleForUpgradeCmd,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				err         error
				clusterFile = upgradeFlags.ClusterFile
			)
			if len(args) == 0 && clusterFile == "" {
				return fmt.Errorf("you must input image name Or use Clusterfile")
			}

			if clusterFile != "" {
				return upgradeWithClusterfile(clusterFile, upgradeFlags)
			}

			imageEngine, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
			if err != nil {
				return err
			}

			id, err := imageEngine.Pull(&options.PullOptions{
				Quiet:      false,
				PullPolicy: "missing",
				Image:      args[0],
				Platform:   "local",
			})
			if err != nil {
				return err
			}

			imageSpec, err := imageEngine.Inspect(&options.InspectOptions{ImageNameOrID: id})
			if err != nil {
				return fmt.Errorf("failed to get sealer image extension: %s", err)
			}

			current, _, err := clusterfile.GetActualClusterFile()
			if err != nil {
				return err
			}

			cluster := current.GetCluster()
			//update image of cluster
			cluster.Spec.APPNames = upgradeFlags.AppNames
			cluster.Spec.Image = args[0]
			clusterData, err := yaml.Marshal(cluster)
			if err != nil {
				return err
			}

			//generate new cluster
			//TODO Potential Bug: new cf will lose previous Clusterfile object such as config,plugins.so new cf will only have v2.Cluster.
			newClusterfile, err := clusterfile.NewClusterFile(clusterData)
			if err != nil {
				return err
			}

			return upgradeCluster(newClusterfile, imageEngine, imageSpec, upgradeFlags)
		},
	}

	upgradeCmd.Flags().StringVarP(&upgradeFlags.ClusterFile, "Clusterfile", "f", "", "Clusterfile path to upgrade a Kubernetes cluster")
	upgradeCmd.Flags().StringSliceVar(&upgradeFlags.AppNames, "apps", nil, "override default AppNames of sealer image")
	upgradeCmd.Flags().BoolVar(&upgradeFlags.IgnoreCache, "ignore-cache", false, "whether ignore cache when distribute sealer image, default is false.")

	return upgradeCmd
}

func upgradeCluster(cf clusterfile.Interface, imageEngine imageengine.Interface, imageSpec *imagev1.ImageSpec, upgradeFlags *types.UpgradeFlags) error {
	if imageSpec.ImageExtension.Type != imagev1.KubeInstaller {
		return fmt.Errorf("exit upgrade process, wrong sealer image type: %s", imageSpec.ImageExtension.Type)
	}

	cluster := cf.GetCluster()
	infraDriver, err := infradriver.NewInfraDriver(utils.MergeClusterWithImageExtension(&cluster, imageSpec.ImageExtension))
	if err != nil {
		return err
	}

	imageName := infraDriver.GetClusterImageName()

	clusterHosts := infraDriver.GetHostIPList()
	clusterHostsPlatform, err := infraDriver.GetHostsPlatform(clusterHosts)
	if err != nil {
		return err
	}

	logrus.Infof("start to upgrade cluster with image: \"%s\"", imageName)

	imageMounter, err := imagedistributor.NewImageMounter(imageEngine, clusterHostsPlatform)
	if err != nil {
		return err
	}

	imageMountInfo, err := imageMounter.Mount(imageName)
	if err != nil {
		return err
	}

	defer func() {
		err = imageMounter.Umount(imageName, imageMountInfo)
		if err != nil {
			logrus.Errorf("failed to umount sealer image")
		}
	}()

	distributor, err := imagedistributor.NewScpDistributor(imageMountInfo, infraDriver, cf.GetConfigs(), imagedistributor.DistributeOption{
		IgnoreCache: upgradeFlags.IgnoreCache,
	})
	if err != nil {
		return err
	}

	plugins, err := loadPluginsFromImage(imageMountInfo)
	if err != nil {
		return err
	}

	if cf.GetPlugins() != nil {
		plugins = append(plugins, cf.GetPlugins()...)
	}

	runtimeConfig := &clusterruntime.RuntimeConfig{
		Distributor:            distributor,
		Plugins:                plugins,
		ContainerRuntimeConfig: cluster.Spec.ContainerRuntime,
	}

	upgrader, err := clusterruntime.NewInstaller(infraDriver, *runtimeConfig, clusterruntime.GetClusterInstallInfo(imageSpec.ImageExtension.Labels, runtimeConfig.ContainerRuntimeConfig))
	if err != nil {
		return err
	}

	//we need to save desired clusterfile to local disk temporarily
	//and will use it later to clean the cluster node if apply failed.
	if err = cf.SaveAll(clusterfile.SaveOptions{}); err != nil {
		return err
	}

	err = upgrader.Upgrade()
	if err != nil {
		return err
	}

	confPath := clusterruntime.GetClusterConfPath(imageSpec.ImageExtension.Labels)
	cmds := infraDriver.GetClusterLaunchCmds()
	appNames := infraDriver.GetClusterLaunchApps()

	// merge to application between v2.ClusterSpec, v2.Application and image extension
	v2App, err := application.NewAppDriver(utils.ConstructApplication(cf.GetApplication(), cmds, appNames, cluster.Spec.Env), imageSpec.ImageExtension)
	if err != nil {
		return fmt.Errorf("failed to parse application from Clusterfile:%v ", err)
	}

	// install application
	if err = v2App.Launch(infraDriver); err != nil {
		return err
	}
	if err = v2App.Save(application.SaveOptions{}); err != nil {
		return err
	}

	//save and commit
	if err = cf.SaveAll(clusterfile.SaveOptions{CommitToCluster: false, ConfPath: confPath}); err != nil {
		return err
	}

	logrus.Infof("succeeded in upgrading cluster with image %s", imageName)

	return nil
}

func upgradeWithClusterfile(clusterFile string, upgradeFlags *types.UpgradeFlags) error {
	clusterFileData, err := os.ReadFile(filepath.Clean(clusterFile))
	if err != nil {
		return err
	}

	cf, err := clusterfile.NewClusterFile(clusterFileData)
	if err != nil {
		return err
	}

	cluster := cf.GetCluster()
	imageName := cluster.Spec.Image
	imageEngine, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
	if err != nil {
		return err
	}

	id, err := imageEngine.Pull(&options.PullOptions{
		Quiet:      false,
		PullPolicy: "missing",
		Image:      imageName,
		Platform:   "local",
	})
	if err != nil {
		return err
	}

	imageSpec, err := imageEngine.Inspect(&options.InspectOptions{ImageNameOrID: id})
	if err != nil {
		return fmt.Errorf("failed to get sealer image extension: %s", err)
	}

	return upgradeCluster(cf, imageEngine, imageSpec, upgradeFlags)
}
