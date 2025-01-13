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
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/ipvs"
	"github.com/sealerio/sealer/pkg/runtime"
	netutils "github.com/sealerio/sealer/utils/net"
	"github.com/sealerio/sealer/utils/shellcommand"
)

const (
	AuditPolicyYml = "audit-policy.yml"
	KubeadmFileYml = "/etc/kubernetes/kubeadm.yaml"
	AdminConf      = "admin.conf"
	ControllerConf = "controller-manager.conf"
	SchedulerConf  = "scheduler.conf"
	KubeletConf    = "kubelet.conf"

	// kube file
	KUBECONTROLLERCONFIGFILE = "/etc/kubernetes/controller-manager.conf"
	KUBESCHEDULERCONFIGFILE  = "/etc/kubernetes/scheduler.conf"
	AdminKubeConfPath        = "/etc/kubernetes/admin.conf"
	LvscarePodFileName       = "kube-lvscare.yaml"
)

const (
	GetCustomizeCRISocket         = "cat /etc/sealerio/cri/socket-path"
	RemoteCleanCustomizeCRISocket = "rm -f /etc/sealerio/cri/socket-path"
	RemoteAddEtcHosts             = "cat /etc/hosts |grep '%s' || echo '%s' >> /etc/hosts"
	RemoteReplaceKubeConfig       = `grep -qF "apiserver.cluster.local" %s  && sed -i 's/apiserver.cluster.local/%s/' %s && sed -i 's/apiserver.cluster.local/%s/' %s`
	RemoveKubeConfig              = "rm -rf /usr/bin/kube* && rm -rf ~/.kube/"
	RemoteCleanK8sOnHost          = `systemctl restart docker kubelet; if which kubeadm > /dev/null 2>&1;then kubeadm reset -f %s;fi && \
rm -rf /etc/kubernetes/ && \
rm -rf /etc/systemd/system/kubelet.service.d && rm -rf /etc/systemd/system/kubelet.service && \
rm -rf /usr/bin/kubeadm && rm -rf /usr/bin/kubelet-pre-start.sh && \
rm -rf /usr/bin/kubelet && rm -rf /usr/bin/kubectl && \
rm -rf /var/lib/kubelet/* && rm -rf /etc/sysctl.d/k8s.conf && \
rm -rf /etc/cni && rm -rf /opt/cni && \
rm -rf /var/lib/etcd/* && rm -rf /var/etcd/* && rm -rf /root/.kube/config
`
	RemoteRemoveAPIServerEtcHost = "echo \"$(sed \"/%s/d\" /etc/hosts)\" > /etc/hosts"
	KubeDeleteNode               = "kubectl delete node %s"

	RemoteCheckRoute = "seautil route check --host %s"
	RemoteAddRoute   = "seautil route add --host %s --gateway %s"
	RemoteDelRoute   = "if command -v seautil > /dev/null 2>&1; then seautil route del --host %s --gateway %s; fi"
)

// StaticFile :static file should not be template, will never be changed while initialization.
type StaticFile struct {
	DestinationDir string
	Name           string
}

// MasterStaticFiles Put static files here, can be moved to all master nodes before kubeadm execution
var MasterStaticFiles = []*StaticFile{
	{
		DestinationDir: "/etc/kubernetes",
		Name:           AuditPolicyYml,
	},
}

// return node name from k8s cluster, if not found, return "" and error is nil
func (k *Runtime) getNodeNameByCmd(host net.IP) (string, error) {
	cli, err := k.GetCurrentRuntimeDriver()
	if err != nil {
		return "", err
	}
	nodes := &corev1.NodeList{}
	if err := cli.List(context.Background(), nodes); err != nil {
		return "", err
	}

	for _, nodeInfo := range nodes.Items {
		for _, addr := range nodeInfo.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP && host.String() == addr.Address {
				return nodeInfo.Name, nil
			}
		}
	}

	return "", fmt.Errorf("failed to find node name for %s", host.String())
}

func vlogToStr(vlog int) string {
	str := strconv.Itoa(vlog)
	return " -v " + str
}

type CommandType string

const InitMaster CommandType = "initMaster"
const JoinMaster CommandType = "joinMaster"
const JoinNode CommandType = "joinNode"

func (k *Runtime) Command(name CommandType, nodeNameOverride string) (string, error) {
	//cmds := make(map[CommandType]string)
	// Please convert your v1beta1 configuration files to v1beta2 using the
	// "kubeadm config migrate" command of kubeadm v1.15.x, so v1.14 not support multi network interface.
	cmds := map[CommandType]string{
		InitMaster: fmt.Sprintf("kubeadm init --config=%s --upload-certs", KubeadmFileYml),
		JoinMaster: fmt.Sprintf("kubeadm join --config=%s", KubeadmFileYml),
		JoinNode:   fmt.Sprintf("kubeadm join --config=%s", KubeadmFileYml),
	}

	v, ok := cmds[name]
	if !ok {
		return "", fmt.Errorf("failed to get kubeadm command: %v", cmds)
	}
	if nodeNameOverride != "" {
		v = fmt.Sprintf("%s --node-name %s", v, nodeNameOverride)
	}

	if runtime.IsInContainer() {
		return fmt.Sprintf("%s%s%s", v, vlogToStr(k.Config.Vlog), " --ignore-preflight-errors=all"), nil
	}
	if name == InitMaster || name == JoinMaster {
		return fmt.Sprintf("%s%s%s", v, vlogToStr(k.Config.Vlog), " --ignore-preflight-errors=SystemVerification,Port-10250,DirAvailable--etc-kubernetes-manifests"), nil
	}

	return fmt.Sprintf("%s%s%s", v, vlogToStr(k.Config.Vlog), " --ignore-preflight-errors=Port-10250,DirAvailable--etc-kubernetes-manifests"), nil
}

func (k *Runtime) getNodeNameOverride(ip net.IP) string {
	if v, ok := k.infra.GetClusterEnv()[common.EnvUseIPasNodeName]; ok && v == "true" {
		return ip.String()
	}

	return ""
}

func GetClientFromConfig(adminConfPath string) (runtimeClient.Client, error) {
	adminConfig, err := clientcmd.BuildConfigFromFlags("", adminConfPath)
	if nil != err {
		return nil, err
	}

	var ret runtimeClient.Client

	timeout := time.Second * 30
	err = wait.PollImmediate(time.Second*10, timeout, func() (done bool, err error) {
		cli, err := runtimeClient.New(adminConfig, runtimeClient.Options{})
		if nil != err {
			return false, err
		}

		ns := corev1.Namespace{}
		if err := cli.Get(context.Background(), runtimeClient.ObjectKey{Name: "default"}, &ns); nil != err {
			return false, err
		}

		ret = cli

		return true, nil
	})

	return ret, err
}

func (k *Runtime) configureLvs(masterHosts, clientHosts []net.IP) error {
	lvsImageURL := common.LvsCareRepoAndTag

	var rs []string
	var realEndpoints []string

	masters := netutils.IPsToIPStrs(masterHosts)
	sort.Strings(masters)
	for _, m := range masters {
		rep := net.JoinHostPort(m, "6443")
		rs = append(rs, fmt.Sprintf("--rs %s", rep))
		realEndpoints = append(realEndpoints, rep)
	}
	vs := net.JoinHostPort(k.getAPIServerVIP().String(), "6443")
	ipvsCmd := fmt.Sprintf("seautil ipvs --vs %s %s --health-path /healthz --health-schem https --run-once", vs, strings.Join(rs, " "))
	y, err := ipvs.LvsStaticPodYaml(common.KubeLvsCareStaticPodName, vs, realEndpoints, lvsImageURL,
		"/healthz", "https")
	if err != nil {
		return err
	}
	lvscareStaticCmd := ipvs.GetCreateLvscareStaticPodCmd(y, LvscarePodFileName)

	eg, _ := errgroup.WithContext(context.Background())

	// flush all cluster nodes as latest ipvs policy.
	for i := range clientHosts {
		node := clientHosts[i]
		eg.Go(func() error {
			err := k.infra.CmdAsync(node, nil, ipvsCmd, lvscareStaticCmd, shellcommand.CommandSetHostAlias(k.getAPIServerDomain(), k.getAPIServerVIP().String()))
			if err != nil {
				return fmt.Errorf("failed to config ndoes lvs policy %s: %v", ipvsCmd, err)
			}
			return nil
		})
	}

	return eg.Wait()
}
