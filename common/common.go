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

package common

import (
	"path/filepath"

	"github.com/mitchellh/go-homedir"
)

const (
	MASTER = "master"
	// TODO，警惕，不能通过此标志来获取worker，因为master也可以role=node
	NODE = "node"
)

const (
	Docker     = "docker"
	Containerd = "containerd"
)

const (
	K0s string = "k0s"
	K3s string = "k3s"
	K8s string = "kubernetes"
)

// Default dir and file path
const (
	DefaultLogDir            = "/var/lib/sealer/log"
	DefaultSealerDataDir     = "/var/lib/sealer/data"
	KubeAdminConf            = "/etc/kubernetes/admin.conf"
	ClusterfileName          = "ClusterfileName"
	KubeLvsCareStaticPodName = "kube-lvscare"
	RegLvsCareStaticPodName  = "reg-lvscare"
	StaticPodDir             = "/etc/kubernetes/manifests"
	LvsCareRepoAndTag        = "docker-cdv5ju.swr-pro.myhuaweicloud.com/global/lvscare:latest"
)

// Envs
const (
	EnvHostIP                 = "HostIP"
	EnvHostIPFamily           = "HostIPFamily"
	EnvContainerRuntime       = "ContainerRuntime"
	EnvIPv6DualStack          = "IPv6DualStack"
	EnvRegistryDomain         = "RegistryDomain"
	EnvRegistryPort           = "RegistryPort"
	EnvRegistryURL            = "RegistryURL"
	EnvLocalRegistryDomain    = "LocalRegistryDomain"
	EnvLocalRegistryPort      = "LocalRegistryPort"
	EnvLocalRegistryURL       = "LocalRegistryURL"
	EnvExternalRegistryDomain = "ExternalRegistryDomain"
	EnvExternalRegistryPort   = "ExternalRegistryPort"
	EnvExternalRegistryURL    = "ExternalRegistryURL"
	EnvCertSANs               = "CertSANs"
	EnvIPvsVIPForIPv4         = "IPvsVIPv4"
	EnvIPvsVIPForIPv6         = "IPvsVIPv6"
	EnvSvcCIDR                = "SvcCIDR"
	EnvPodCIDR                = "PodCIDR"
	EnvDNSSvcIP               = "DNSSvcIP"
	EnvKubeSvcIP              = "KubeSvcIP"
	EnvUseIPasNodeName        = "UseIPasNodeName"
)

const (
	MasterRoleLabel = "node-role.kubernetes.io/master"
)

const (
	ApplyModeApply     = "apply"
	ApplyModeLoadImage = "loadImage"
)

// image module
const (
	DefaultMetadataName         = "Metadata"
	DefaultRegistryDomain       = "sea.hub"
	DefaultRegistryPort         = 5000
	DefaultRegistryURL          = "sea.hub:5000"
	DefaultRegistryHtPasswdFile = "registry_htpasswd"
)

// about infra
const (
	AliDomain       = "sea.aliyun.com/"
	Eip             = AliDomain + "ClusterEIP"
	RegistryDirName = "registry"
)

// CRD kind
const (
	KubeletConfiguration   = "KubeletConfiguration"
	KubeProxyConfiguration = "KubeProxyConfiguration"
)

// plugin type
const (
	TAINT    = "TAINT"
	LABEL    = "LABEL"
	HOSTNAME = "HOSTNAME"
)

// default cluster runtime configuration
const (
	DefaultVIP             = "10.103.97.2"
	DefaultVIPForIPv6      = "1248:4003:10bb:6a01:83b9:6360:c66d:0002"
	DefaultAPIserverDomain = "apiserver.cluster.local"
)

const (
	BAREMETAL = "BAREMETAL"
	AliCloud  = "ALI_CLOUD"
	CONTAINER = "CONTAINER"
)

const (
	FileMode0755 = 0755
	FileMode0644 = 0644
)

const APIServerDomain = "apiserver.cluster.local"

const (
	CdAndExecCmd        = "cd %s && %s"
	CdIfExistAndExecCmd = "if [ ! -d %s ];then exit 0;fi; cd %s && %s"
)

const (
	ExecBinaryFileName = "sealer"
	ROOT               = "root"
	WINDOWS            = "windows"
)

func GetSealerWorkDir() string {
	return filepath.Join(GetHomeDir(), ".sealer")
}

func GetDefaultClusterfile() string {
	return filepath.Join(GetSealerWorkDir(), "Clusterfile")
}

func GetDefaultApplicationFile() string {
	return filepath.Join(GetSealerWorkDir(), "application.json")
}

func DefaultRegistryAuthConfigDir() string {
	return filepath.Join(GetHomeDir(), ".docker/config.json")
}

func DefaultKubeConfigDir() string {
	return filepath.Join(GetHomeDir(), ".kube")
}

func GetHomeDir() string {
	home, err := homedir.Dir()
	if err != nil {
		return "/root"
	}
	return home
}
