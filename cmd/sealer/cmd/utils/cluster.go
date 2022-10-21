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

package utils

import (
	"fmt"
	"net"
	"reflect"
	"strconv"

	"github.com/sealerio/sealer/cmd/sealer/cmd/types"

	netutils "github.com/sealerio/sealer/utils/net"
	strUtils "github.com/sealerio/sealer/utils/strings"

	"github.com/sealerio/sealer/common"
	v1 "github.com/sealerio/sealer/types/api/v1"
	v2 "github.com/sealerio/sealer/types/api/v2"
)

func ConstructClusterForRun(imageName string, runFlags *types.Flags) (*v2.Cluster, error) {
	resultHosts, err := TransferIPStrToHosts(runFlags.Masters, runFlags.Nodes)
	if err != nil {
		return nil, err
	}

	cluster := v2.Cluster{
		Spec: v2.ClusterSpec{
			SSH: v1.SSH{
				User:     runFlags.User,
				Passwd:   runFlags.Password,
				PkPasswd: runFlags.PkPassword,
				Pk:       runFlags.Pk,
				Port:     strconv.Itoa(int(runFlags.Port)),
			},
			Image:   imageName,
			Hosts:   resultHosts,
			Env:     runFlags.CustomEnv,
			CMDArgs: runFlags.CMDArgs,
		},
	}
	cluster.APIVersion = common.APIVersion
	cluster.Kind = common.Kind
	cluster.Name = "my-cluster"
	return &cluster, nil
}

func ConstructClusterForScaleUp(cluster *v2.Cluster, scaleFlags *types.Flags, joinMasters, joinWorkers []net.IP) error {
	// merge custom Env to the existed cluster
	cluster.Spec.Env = append(cluster.Spec.Env, scaleFlags.CustomEnv...)
	//todo Add password encryption mode in the future
	//add joined masters
	if len(joinMasters) != 0 {
		masterIPs := cluster.GetMasterIPList()
		for _, ip := range joinMasters {
			// if ip already taken by master will return join duplicated ip error
			if netutils.IsInIPList(ip, masterIPs) {
				return fmt.Errorf("failed to scale master for duplicated ip: %s", ip)
			}
		}
		host := constructHost(common.MASTER, joinMasters, scaleFlags, cluster.Spec.SSH)
		cluster.Spec.Hosts = append(cluster.Spec.Hosts, host)
	}

	//add joined nodes
	if len(joinWorkers) != 0 {
		nodeIPs := cluster.GetNodeIPList()
		for _, ip := range joinWorkers {
			// if ip already taken by node will return join duplicated ip error
			if netutils.IsInIPList(ip, nodeIPs) {
				return fmt.Errorf("failed to scale node for duplicated ip: %s", ip)
			}
		}

		host := constructHost(common.NODE, joinWorkers, scaleFlags, cluster.Spec.SSH)
		cluster.Spec.Hosts = append(cluster.Spec.Hosts, host)
	}
	return nil
}

func ConstructClusterForScaleDown(cluster *v2.Cluster, mastersToDelete, workersToDelete []net.IP) error {
	if len(mastersToDelete) != 0 {
		for i := range cluster.Spec.Hosts {
			if strUtils.IsInSlice(common.MASTER, cluster.Spec.Hosts[i].Roles) {
				cluster.Spec.Hosts[i].IPS = removeIPList(cluster.Spec.Hosts[i].IPS, mastersToDelete)
			}
			continue
		}
	}

	if len(workersToDelete) != 0 {
		for i := range cluster.Spec.Hosts {
			if strUtils.IsInSlice(common.NODE, cluster.Spec.Hosts[i].Roles) {
				cluster.Spec.Hosts[i].IPS = removeIPList(cluster.Spec.Hosts[i].IPS, workersToDelete)
			}
			continue
		}
	}

	// if hosts have no ip address exist,then delete this host.
	var hosts []v2.Host
	for _, host := range cluster.Spec.Hosts {
		if len(host.IPS) == 0 {
			continue
		}
		hosts = append(hosts, host)
	}
	cluster.Spec.Hosts = hosts

	return nil
}

func constructHost(role string, joinIPs []net.IP, scaleFlags *types.Flags, clusterSSH v1.SSH) v2.Host {
	//todo we could support host level env form cli later.
	//todo we could support host level role form cli later.
	host := v2.Host{
		IPS:   joinIPs,
		Roles: []string{role},
	}

	scaleFlagSSH := v1.SSH{
		User:     scaleFlags.User,
		Passwd:   scaleFlags.Password,
		Port:     strconv.Itoa(int(scaleFlags.Port)),
		Pk:       scaleFlags.Pk,
		PkPasswd: scaleFlags.PkPassword,
	}

	if reflect.DeepEqual(scaleFlagSSH, clusterSSH) {
		return host
	}

	host.SSH = scaleFlagSSH
	return host
}

func removeIPList(clusterIPList []net.IP, toBeDeletedIPList []net.IP) (res []net.IP) {
	for _, ip := range clusterIPList {
		if !netutils.IsInIPList(ip, toBeDeletedIPList) {
			res = append(res, ip)
		}
	}
	return
}
