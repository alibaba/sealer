// Copyright © 2022 Alibaba Group Holding Ltd.
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

package clusterfile

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/kube-proxy/config/v1alpha1"
	"k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm/v1beta2"

	"github.com/sealerio/sealer/common"
	v1 "github.com/sealerio/sealer/types/api/v1"
	v2 "github.com/sealerio/sealer/types/api/v2"
)

func decodeClusterFile(reader io.Reader, clusterfile *ClusterFile) error {
	decoder := yaml.NewYAMLToJSONDecoder(bufio.NewReaderSize(reader, 4096))

	for {
		ext := runtime.RawExtension{}
		if err := decoder.Decode(&ext); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}
		metaType := metav1.TypeMeta{}
		if err := yaml.Unmarshal(ext.Raw, &metaType); err != nil {
			return fmt.Errorf("failed to decode TypeMeta: %v", err)
		}

		switch metaType.Kind {
		case common.Cluster:
			var cluster v2.Cluster

			if err := yaml.Unmarshal(ext.Raw, &cluster); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}
			if err := checkAndCompleteCluster(&cluster); err != nil {
				return fmt.Errorf("failed to check and complete cluster: %v", err)
			}

			clusterfile.cluster = &cluster
		case common.Config:
			var cfg v1.Config

			if err := yaml.Unmarshal(ext.Raw, &cfg); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}
			clusterfile.configs = append(clusterfile.configs, cfg)
		case common.Plugin:
			var plu v1.Plugin

			if err := yaml.Unmarshal(ext.Raw, &plu); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.plugins = append(clusterfile.plugins, plu)
		case common.InitConfiguration:
			var in v1beta2.InitConfiguration

			if err := yaml.Unmarshal(ext.Raw, &in); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.kubeadmConfig.InitConfiguration = in
		case common.JoinConfiguration:
			var in v1beta2.JoinConfiguration

			if err := yaml.Unmarshal(ext.Raw, &in); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.kubeadmConfig.JoinConfiguration = in
		case common.ClusterConfiguration:
			var in v1beta2.ClusterConfiguration

			if err := yaml.Unmarshal(ext.Raw, &in); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.kubeadmConfig.ClusterConfiguration = in
		case common.KubeletConfiguration:
			var in v1beta1.KubeletConfiguration

			if err := yaml.Unmarshal(ext.Raw, &in); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.kubeadmConfig.KubeletConfiguration = in
		case common.KubeProxyConfiguration:
			var in v1alpha1.KubeProxyConfiguration

			if err := yaml.Unmarshal(ext.Raw, &in); err != nil {
				return fmt.Errorf("failed to decode %s[%s]: %v", metaType.Kind, metaType.APIVersion, err)
			}

			clusterfile.kubeadmConfig.KubeProxyConfiguration = in
		}
	}
}

func checkAndCompleteCluster(cluster *v2.Cluster) error {
	// check registry config is valid,
	// make sure external registry domain is valid
	if cluster.Spec.Registry.ExternalRegistry != nil {
		if cluster.Spec.Registry.ExternalRegistry.Domain == "" {
			return fmt.Errorf("external registry domain can not be empty")
		}
	}

	if cluster.Spec.Registry.LocalRegistry != nil {
		if cluster.Spec.Registry.LocalRegistry.Domain == "" {
			cluster.Spec.Registry.LocalRegistry.Domain = common.DefaultRegistryDomain
		}
		if cluster.Spec.Registry.LocalRegistry.Port == 0 {
			cluster.Spec.Registry.LocalRegistry.Port = common.DefaultRegistryPort
		}
	}

	if cluster.Spec.Registry.LocalRegistry == nil && cluster.Spec.Registry.ExternalRegistry == nil {
		cluster.Spec.Registry.LocalRegistry = &v2.LocalRegistry{
			InsecureMode: false,
			HaMode:       true,
		}
		cluster.Spec.Registry.LocalRegistry.RegistryConfig = v2.RegistryConfig{
			Domain: common.DefaultRegistryDomain,
			Port:   common.DefaultRegistryPort,
		}
	}

	regConfig := v2.RegistryConfig{}
	if cluster.Spec.Registry.ExternalRegistry != nil {
		regConfig = cluster.Spec.Registry.ExternalRegistry.RegistryConfig
	}
	if cluster.Spec.Registry.LocalRegistry != nil {
		regConfig = cluster.Spec.Registry.LocalRegistry.RegistryConfig
	}

	var newEnv []string
	for _, env := range cluster.Spec.Env {
		if strings.HasPrefix(env, common.EnvRegistryDomain) || strings.HasPrefix(env, common.EnvRegistryPort) || strings.HasPrefix(env, common.EnvRegistryURL) {
			continue
		}
		newEnv = append(newEnv, env)
	}
	cluster.Spec.Env = newEnv
	cluster.Spec.Env = append(cluster.Spec.Env, fmt.Sprintf("%s=%s", common.EnvRegistryDomain, regConfig.Domain))
	cluster.Spec.Env = append(cluster.Spec.Env, fmt.Sprintf("%s=%d", common.EnvRegistryPort, regConfig.Port))
	cluster.Spec.Env = append(cluster.Spec.Env, fmt.Sprintf("%s=%s", common.EnvRegistryURL, net.JoinHostPort(regConfig.Domain, strconv.Itoa(regConfig.Port))))

	return nil
}
