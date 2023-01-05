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

package alpha

import (
	"context"
	"fmt"
	"strings"

	reference2 "github.com/distribution/distribution/v3/reference"
	"github.com/liushuochen/gotable"
	"github.com/spf13/cobra"

	"github.com/sealerio/sealer/pkg/image/reference"
	save2 "github.com/sealerio/sealer/pkg/image/save"
)

const (
	imageName = "IMAGE NAME"
	version   = "VERSION"
	Network   = "NETWORK-PLUGINS"
)

var longNewSearchCmdDescription = ``

var exampleForSearchCmd = `sealer alpha search <imageDomain>/<imageRepo>/<imageName> ...
## default imageDomain: 'docker.io', default imageRepo: 'sealerio'
ex.:
  sealer alpha search kubernetes
`

// NewSearchCmd searchCmd represents the search command
func NewSearchCmd() *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "search ClusterImage in default registry",
		// TODO: add long description.
		Long:    longNewSearchCmdDescription,
		Example: exampleForSearchCmd,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			table, err := gotable.Create(imageName, version, Network)
			if err != nil {
				return err
			}
			for _, imgName := range args {
				named, err := reference.ParseToNamed(imgName)
				if err != nil {
					return fmt.Errorf("repository does not exist, err: %v", err)
				}
				ns, err := save2.NewProxyRegistry(context.Background(), "", named.Domain())
				if err != nil {
					return err
				}
				rNamed, err := reference2.WithName(named.Repo())
				if err != nil {
					return fmt.Errorf("failed to get repository name: %v", err)
				}
				repo, err := ns.Repository(context.Background(), rNamed)
				if err != nil {
					return err
				}
				tags, err := repo.Tags(context.Background()).All(context.Background())
				if err != nil {
					return err
				}
				for _, tag := range tags {
					if strings.Contains(tag, "-") {
						split := strings.Split(tag, "-")
						if err := table.AddRow([]string{named.String(), tag, split[1]}); err != nil {
							return err
						}
					} else {
						if err := table.AddRow([]string{named.String(), tag, "calico"}); err != nil {
							return err
						}
					}
				}
			}
			fmt.Println(table)
			return nil
		},
	}
	return searchCmd
}
