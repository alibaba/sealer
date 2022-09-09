// Copyright © 2021 Alibaba Group Holding Ltd.
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

package kubernetes

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/sealerio/sealer/common"
	containerruntime "github.com/sealerio/sealer/pkg/container-runtime"
	"github.com/sealerio/sealer/pkg/infradriver"
	"github.com/sealerio/sealer/pkg/registry"
	"github.com/sealerio/sealer/pkg/runtime"
	"github.com/sealerio/sealer/pkg/runtime/kubernetes/kubeadm_config"
	"github.com/sealerio/sealer/utils"
	utilsnet "github.com/sealerio/sealer/utils/net"

	"github.com/sirupsen/logrus"
)

var ForceDelete bool

type Config struct {
	Vlog                         int
	VIP                          string
	RegistryInfo                 registry.Info
	containerRuntimeInfo         containerruntime.Info
	KubeadmConfigFromClusterFile kubeadm_config.KubeadmConfig
	LvsImage                     string
	APIServerDomain              string
}

//Runtime struct is the runtime interface for kubernetes
type Runtime struct {
	infra  infradriver.InfraDriver
	Config *Config
}

func NewKubeadmRuntime(clusterFileKubeConfig kubeadm_config.KubeadmConfig, infra infradriver.InfraDriver, containerRuntimeInfo containerruntime.Info, registryInfo registry.Info) (runtime.Installer, error) {
	k := &Runtime{
		infra: infra,
		Config: &Config{
			KubeadmConfigFromClusterFile: clusterFileKubeConfig,
			APIServerDomain:              DefaultAPIserverDomain,
			//TODO
			LvsImage:             fmt.Sprintf("%s/fanux/lvscare:latest", registryInfo.URL),
			VIP:                  DefaultVIP,
			RegistryInfo:         registryInfo,
			containerRuntimeInfo: containerRuntimeInfo,
		},
	}

	if logrus.GetLevel() == logrus.DebugLevel {
		k.Config.Vlog = 6
	}

	return k, nil
}

func (k *Runtime) Install() error {
	masters := k.infra.GetHostIPListByRole(common.MASTER)
	workers := k.infra.GetHostIPListByRole(common.NODE)

	kubeadmConf, err := k.initKubeadmConfig(masters)
	if err != nil {
		return err
	}

	if err = k.generateCert(kubeadmConf, masters[0]); err != nil {
		return err
	}

	if err = k.createKubeConfig(masters[0]); err != nil {
		return err
	}

	if err = k.CopyStaticFiles(masters[0:1]); err != nil {
		return err
	}

	token, certKey, err := k.initMaster0(kubeadmConf, masters[0])
	if err != nil {
		return err
	}

	if err = k.joinMasters(masters[1:], masters[0], kubeadmConf, token, certKey); err != nil {
		return err
	}

	if err = k.joinNodes(workers, masters, kubeadmConf, token); err != nil {
		return err
	}

	if err := k.dumpKubeConfigIntoCluster(masters[0]); err != nil {
		return err
	}

	return nil
}

func (k *Runtime) GetCurrentRuntimeDriver() (runtime.Driver, error) {
	return NewKubeDriver(AdminKubeConfPath)
}

func (k *Runtime) Upgrade() error {
	panic("now not support upgrade")
}

func (k *Runtime) Reset() error {
	masters := k.infra.GetHostIPListByRole(common.MASTER)
	workers := k.infra.GetHostIPListByRole(common.NODE)

	if err := confirmDeleteHosts(fmt.Sprintf("%s/%s", common.MASTER, common.NODE), append(masters, workers...)); err != nil {
		return err
	}

	if err := k.deleteNodes(workers, []net.IP{}); err != nil {
		return err
	}

	if err := k.deleteMasters(masters, []net.IP{}, []net.IP{}); err != nil {
		return err
	}

	return nil
}

func (k *Runtime) ScaleUp(newMasters, newWorkers []net.IP) error {
	masters := k.infra.GetHostIPListByRole(common.MASTER)

	kubeadmConfig, err := kubeadm_config.LoadKubeadmConfigs(KubeadmFileYml, utils.DecodeCRDFromFile)
	if err != nil {
		return err
	}

	token, certKey, err := k.getJoinTokenHashAndKey(masters[0])
	if err != nil {
		return err
	}

	if err = k.joinMasters(newMasters, masters[0], kubeadmConfig, token, certKey); err != nil {
		return err
	}

	if err = k.joinNodes(newWorkers, masters, kubeadmConfig, token); err != nil {
		return err
	}

	return nil
}

func (k *Runtime) ScaleDown(mastersToDelete, workersToDelete []net.IP) error {
	masters := k.infra.GetHostIPListByRole(common.MASTER)
	workers := k.infra.GetHostIPListByRole(common.NODE)

	remainMasters := utilsnet.RemoveIPs(masters, mastersToDelete)
	if len(remainMasters) == 0 {
		return fmt.Errorf("cleaning up all masters is illegal, unless you give the --all flag, which will delete the entire cluster")
	}

	if len(workersToDelete) > 0 {
		if err := confirmDeleteHosts(common.NODE, workersToDelete); err != nil {
			return err
		}

		if err := k.deleteNodes(workersToDelete, remainMasters); err != nil {
			return err
		}
	}

	if len(mastersToDelete) > 0 {
		if err := confirmDeleteHosts(common.MASTER, mastersToDelete); err != nil {
			return err
		}

		remainWorkers := utilsnet.RemoveIPs(workers, workersToDelete)

		if err := k.deleteMasters(mastersToDelete, remainMasters, remainWorkers); err != nil {
			return err
		}
	}

	return nil
}

// /var/lib/sealer/data/my-cluster/certs
func (k *Runtime) getCertsDir() string {
	return filepath.Join(k.infra.GetClusterRootfs(), "certs")
}

// /var/lib/sealer/data/my-cluster/pki
func (k *Runtime) getPKIPath() string {
	return filepath.Join(k.infra.GetClusterRootfs(), "pki")
}

// /var/lib/sealer/data/my-cluster/pki/etcd
func (k *Runtime) getEtcdCertPath() string {
	return filepath.Join(k.getPKIPath(), "etcd")
}

// /var/lib/sealer/data/my-cluster/rootfs/statics
func (k *Runtime) getStaticFileDir() string {
	return filepath.Join(k.infra.GetClusterRootfs(), "statics")
}

func (k *Runtime) getAPIServerDomain() string {
	return k.Config.APIServerDomain
}

func (k *Runtime) getAPIServerVIP() net.IP {
	return net.ParseIP(k.Config.VIP)
}
