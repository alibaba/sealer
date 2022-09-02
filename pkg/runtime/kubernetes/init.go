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
	"context"
	"fmt"
	"github.com/sealerio/sealer/pkg/runtime/kubernetes/kubeadm_config"
	"github.com/sealerio/sealer/pkg/runtime/kubernetes/kubeadm_config/v1beta2"
	"github.com/sealerio/sealer/utils/shellcommand"
	"net"
	"path/filepath"
	"strings"

	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/clustercert"
	"github.com/sealerio/sealer/utils/yaml"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	RemoteCmdCopyStatic    = "mkdir -p %s && cp -f %s %s"
	DefaultVIP             = "10.103.97.2"
	DefaultAPIserverDomain = "apiserver.cluster.local"
	DockerCertDir          = "/etc/docker/certs.d"
)

func (k *Runtime) initKubeadmConfig(masters []net.IP) (kubeadm_config.KubeadmConfig, error) {
	conf, err := kubeadm_config.NewKubeadmConfig(
		k.Config.KubeadmConfigFromClusterFile,
		k.getDefaultKubeadmConfig(),
		masters,
		k.getAPIServerDomain(),
		k.Config.containerRuntimeInfo.Config.CgroupDriver,
		k.getAPIServerVIP())
	if err != nil {
		return kubeadm_config.KubeadmConfig{}, err
	}

	bs, err := yaml.MarshalWithDelimiter(&conf.InitConfiguration,
		&conf.ClusterConfiguration,
		&conf.KubeletConfiguration,
		&conf.KubeProxyConfiguration)
	if err != nil {
		return kubeadm_config.KubeadmConfig{}, err
	}

	//TODO, save it into kubernetes
	cmd := fmt.Sprintf("echo '%s' > %s", string(bs), KubeadmFileYml)
	if err := k.infra.CmdAsync(masters[0], cmd); err != nil {
		return kubeadm_config.KubeadmConfig{}, err
	}

	return conf, nil
}

// /var/lib/sealer/data/my-cluster/mount/etc/kubeadm.yml
func (k *Runtime) getDefaultKubeadmConfig() string {
	return filepath.Join(k.infra.GetClusterRootfs(), "etc", "kubeadm.yml")
}

func (k *Runtime) generateCert(kubeadmConf kubeadm_config.KubeadmConfig, master0 net.IP) error {
	hostName, err := k.infra.GetHostName(master0)
	if err != nil {
		return err
	}

	return clustercert.GenerateAllKubernetesCerts(
		k.getPKIPath(),
		k.getEtcdCertPath(),
		hostName,
		kubeadmConf.GetSvcCIDR(),
		kubeadmConf.GetDNSDomain(),
		kubeadmConf.GetCertSANS(),
		master0,
	)
}

func (k *Runtime) initKubeletService(hosts []net.IP) error {
	initKubeletCmd := fmt.Sprintf("bash %s", filepath.Join(k.infra.GetClusterRootfs(), "scripts", "ini-kube.sh"))

	eg, _ := errgroup.WithContext(context.Background())
	for _, h := range hosts {
		host := h
		eg.Go(func() error {
			if err := k.infra.CmdAsync(host, initKubeletCmd); err != nil {
				return fmt.Errorf("failed to init Kubelet Service on (%s): %s", host, err.Error())
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

func (k *Runtime) createKubeConfig(master0 net.IP) error {
	hostName, err := k.infra.GetHostName(master0)
	if err != nil {
		return err
	}

	controlPlaneEndpoint := fmt.Sprintf("https://%s:6443", k.getAPIServerDomain())

	return clustercert.CreateJoinControlPlaneKubeConfigFiles(k.infra.GetClusterRootfs(), k.getPKIPath(),
		"ca", hostName, controlPlaneEndpoint, "kubernetes")
}

func (k *Runtime) CopyStaticFiles(nodes []net.IP) error {
	for _, file := range MasterStaticFiles {
		staticFilePath := filepath.Join(k.getStaticFileDir(), file.Name)
		cmdLinkStatic := fmt.Sprintf(RemoteCmdCopyStatic, file.DestinationDir, staticFilePath, filepath.Join(file.DestinationDir, file.Name))
		eg, _ := errgroup.WithContext(context.Background())
		for _, host := range nodes {
			host := host
			eg.Go(func() error {
				if err := k.infra.CmdAsync(host, cmdLinkStatic); err != nil {
					return fmt.Errorf("[%s] failed to link static file: %s", host, err.Error())
				}

				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
	}
	return nil
}

//decode output to join token hash and key
func (k *Runtime) decodeMaster0Output(output []byte) (v1beta2.BootstrapTokenDiscovery, string) {
	s0 := string(output)
	logrus.Debugf("decodeOutput: %s", s0)
	slice := strings.Split(s0, "kubeadm join")
	slice1 := strings.Split(slice[1], "Please note")
	logrus.Infof("join command is: kubeadm join %s", slice1[0])

	return k.decodeJoinCmd(slice1[0])
}

//  192.168.0.200:6443 --token 9vr73a.a8uxyaju799qwdjv --discovery-token-ca-cert-hash sha256:7c2e69131a36ae2a042a339b33381c6d0d43887e2de83720eff5359e26aec866 --experimental-control-plane --certificate-key f8902e114ef118304e561c3ecd4d0b543adc226b7a07f675f56564185ffe0c07
func (k *Runtime) decodeJoinCmd(cmd string) (v1beta2.BootstrapTokenDiscovery, string) {
	logrus.Debugf("[globals]decodeJoinCmd: %s", cmd)
	stringSlice := strings.Split(cmd, " ")

	token := v1beta2.BootstrapTokenDiscovery{}
	var certKey string

	for i, r := range stringSlice {
		// upstream error, delete \t, \\, \n, space.
		r = strings.ReplaceAll(r, "\t", "")
		r = strings.ReplaceAll(r, "\n", "")
		r = strings.ReplaceAll(r, "\\", "")
		r = strings.TrimSpace(r)
		if strings.Contains(r, "--token") {
			token.Token = stringSlice[i+1]
		}
		if strings.Contains(r, "--discovery-token-ca-cert-hash") {
			token.CACertHashes = []string{stringSlice[i+1]}
		}
		if strings.Contains(r, "--certificate-key") {
			certKey = stringSlice[i+1][:64]
		}
	}

	return token, certKey
}

//initMaster0 is using kubeadm init to start up the cluster master0.
func (k *Runtime) initMaster0(kubeadmConf kubeadm_config.KubeadmConfig, master0 net.IP) (v1beta2.BootstrapTokenDiscovery, string, error) {
	if err := k.SendJoinMasterKubeConfigs([]net.IP{master0}, kubeadmConf.KubernetesVersion, AdminConf, ControllerConf, SchedulerConf, KubeletConf); err != nil {
		return v1beta2.BootstrapTokenDiscovery{}, "", err
	}

	if err := k.infra.CmdAsync(master0, shellcommand.CommandSetHostAlias(k.getAPIServerDomain(), master0.String())); err != nil {
		return v1beta2.BootstrapTokenDiscovery{}, "", fmt.Errorf("failed to config cluster hosts file cmd: %v", err)
	}

	cmdInit, err := k.Command(kubeadmConf.KubernetesVersion, master0.String(), InitMaster, v1beta2.BootstrapTokenDiscovery{}, "")
	if err != nil {
		return v1beta2.BootstrapTokenDiscovery{}, "", err
	}

	// TODO skip docker version error check for test
	output, err := k.infra.Cmd(master0, cmdInit)
	if err != nil {
		_, wErr := common.StdOut.WriteString(string(output))
		if wErr != nil {
			return v1beta2.BootstrapTokenDiscovery{}, "", err
		}
		return v1beta2.BootstrapTokenDiscovery{}, "", fmt.Errorf("failed to init master0: %s. Please clean and reinstall", err)
	}

	if err := k.infra.CmdAsync(master0, "rm -rf .kube/config && mkdir -p /root/.kube && cp /etc/kubernetes/admin.conf /root/.kube/config"); err != nil {
		return v1beta2.BootstrapTokenDiscovery{}, "", err
	}

	token, certKey := k.decodeMaster0Output(output)

	return token, certKey, nil
}
