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

package infradriver

import (
	v1 "github.com/sealerio/sealer/types/api/v1"
	"net"
)

func GetHostsPlatform(hosts []net.IP, driver InfraDriver) (map[v1.Platform][]net.IP, error) {
	hostsPlatformMap := make(map[v1.Platform][]net.IP)

	for _, ip := range hosts {
		plat, err := driver.GetHostPlatform(ip)
		if err != nil {
			return nil, err
		}

		_, ok := hostsPlatformMap[plat]
		if !ok {
			hostsPlatformMap[plat] = []net.IP{ip}
		} else {
			hostsPlatformMap[plat] = append(hostsPlatformMap[plat], ip)
		}
	}

	return hostsPlatformMap, nil
}
