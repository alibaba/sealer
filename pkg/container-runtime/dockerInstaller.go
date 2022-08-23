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

package container_runtime

import (
	"fmt"
	"net"

	"github.com/sealerio/sealer/pkg/infradriver"
)

type DockerInstaller struct {
	Info   Info
	rootfs string
	driver infradriver.InfraDriver
}

func (d *DockerInstaller) InstallOn(hosts []net.IP) (*Info, error) {
	RemoteChmod := "cd %s  && chmod +x scripts/* && cd scripts && bash docker.sh /var/lib/docker %s %s"
	info := &Info{
		Config{
			Docker,
			DefaultLimitNoFile,
			DefaultSystemdDriver,
		},
		DefaultDockerSocket,
	}
	for _, ip := range hosts {
		initCmd := fmt.Sprintf(RemoteChmod, d.rootfs, d.Info.CgroupDriver, d.Info.LimitNofile)
		err := d.driver.CmdAsync(ip, initCmd)
		if err != nil {
			return nil, fmt.Errorf("failed to exec the install docker init command remote: %s", err)
		}
	}
	return info, nil
}

func (d *DockerInstaller) UnInstallFrom(hosts []net.IP) error {
	CleanCmd := "cd %s  && chmod +x scripts/* && cd scripts && bash docker-uninstall.sh"
	for _, ip := range hosts {
		err := d.driver.CmdAsync(ip, CleanCmd)
		if err != nil {
			return fmt.Errorf("failed to exec clean docker command remote: %s", err)
		}
	}
	return nil
}
