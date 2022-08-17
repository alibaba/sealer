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

package registry

import (
	"context"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"
	"net"
)

func GenerateHTTPBasicAuth(username, password string) (string, error) {
	if username == "" || password == "" {
		return "", fmt.Errorf("failed to generate HTTP basic authentication: registry username or password is empty")
	}
	pwdHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to generate registry password: %v", err)
	}
	return username + ":" + string(pwdHash), nil
}

func concurrencyExecute(f func(host net.IP) error, ips []net.IP) error {
	eg, _ := errgroup.WithContext(context.Background())
	for _, ip := range ips {
		host := ip
		eg.Go(func() error {
			err := f(host)
			if err != nil {
				return fmt.Errorf("on host [%s]: %v", ip.String(), err)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}
