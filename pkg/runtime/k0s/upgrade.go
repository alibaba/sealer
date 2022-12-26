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

package k0s

import (
	"fmt"
	"net"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

func (k *Runtime) upgrade() error {
	var err error
	binPath := filepath.Join(k.getRootfs(), `bin`)

	/** To upgrade a node
	STEP1: prepare a k0s bin file with expected version
	STEP2: move k0s bin to /usr/bin
	STEP3: stop k0s service
	STEP4: start k0s service
	*/
	err = k.upgradeMasters([]net.IP{k.cluster.GetMaster0IP()}, binPath)
	if err != nil {
		return err
	}
	err = k.upgradeMasters(k.cluster.GetMasterIPList()[1:], binPath)
	if err != nil {
		return err
	}
	err = k.upgradeNodes(k.cluster.GetNodeIPList(), binPath)
	if err != nil {
		return err
	}
	return nil
}

func (k *Runtime) upgradeMasters(IPs []net.IP, binPath string) error {
	var cmds = []string{
		fmt.Sprintf("chmod +x %s/*", binPath),
		fmt.Sprintf("mv %s/* /usr/bin", binPath),
		"k0s stop",
		"k0s start",
	}

	for _, ip := range IPs {
		logrus.Infof("start to upgrade master %s", ip)

		ssh, err := k.getHostSSHClient(ip)
		if err != nil {
			return fmt.Errorf("failed to get master ssh client: %v", err)
		}
		if err := ssh.CmdAsync(ip, cmds...); err != nil {
			return err
		}
	}

	return nil
}

func (k *Runtime) upgradeNodes(IPs []net.IP, binPath string) error {
	var nodeCmds = []string{
		fmt.Sprintf("chmod +x %s/*", binPath),
		fmt.Sprintf("mv %s/* /usr/bin", binPath),
		"k0s stop",
		"k0s start",
	}
	var err error
	for _, ip := range IPs {
		logrus.Infof("start to upgrade node %s", ip)

		ssh, err := k.getHostSSHClient(ip)
		if err != nil {
			return fmt.Errorf("failed to get ssh client of host(%s): %v", ip, err)
		}
		err = ssh.CmdAsync(ip, nodeCmds...)
		if err != nil {
			return err
		}
	}
	return err
}
