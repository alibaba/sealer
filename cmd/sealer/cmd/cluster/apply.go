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

package cluster

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/sealerio/sealer/cmd/sealer/cmd/types"
	"github.com/sealerio/sealer/cmd/sealer/cmd/utils"
	"github.com/sealerio/sealer/common"
	"github.com/sealerio/sealer/pkg/clusterfile"
	v12 "github.com/sealerio/sealer/pkg/define/image/v1"
	"github.com/sealerio/sealer/pkg/define/options"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/sealerio/sealer/pkg/infradriver"
	"github.com/sealerio/sealer/utils/strings"
)

var applyFlags *types.ApplyFlags

var longApplyCmdDescription = `apply command is used to apply a Kubernetes cluster via specified Clusterfile.
If the Clusterfile is applied first time, Kubernetes cluster will be created. Otherwise, sealer
will apply the diff change of current Clusterfile and the original one.`

var exampleForApplyCmd = `
  sealer apply -f Clusterfile
`

func NewApplyCmd() *cobra.Command {
	applyCmd := &cobra.Command{
		Use:     "apply",
		Short:   "apply a Kubernetes cluster via specified Clusterfile",
		Long:    longApplyCmdDescription,
		Example: exampleForApplyCmd,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				cf               clusterfile.Interface
				clusterFileData  []byte
				err              error
				applyClusterFile = applyFlags.ClusterFile
				applyMode        = applyFlags.Mode
			)
			logrus.Warn("sealer apply command will be deprecated in the future, please use sealer run instead.")

			if applyClusterFile == "" {
				return fmt.Errorf("you must input Clusterfile")
			}

			clusterFileData, err = os.ReadFile(filepath.Clean(applyClusterFile))
			if err != nil {
				return err
			}

			cf, err = clusterfile.NewClusterFile(clusterFileData)
			if err != nil {
				return err
			}

			desiredCluster := cf.GetCluster()

			// use image extension to determine apply type:
			// scale up cluster, install applications, maybe support upgrade later
			imageName := desiredCluster.Spec.Image
			imageEngine, err := imageengine.NewImageEngine(options.EngineGlobalConfigurations{})
			if err != nil {
				return err
			}

			if err = imageEngine.Pull(&options.PullOptions{
				Quiet:      false,
				PullPolicy: "missing",
				Image:      imageName,
				Platform:   "local",
			}); err != nil {
				return err
			}

			extension, err := imageEngine.GetSealerImageExtension(&options.GetImageAnnoOptions{ImageNameOrID: imageName})
			if err != nil {
				return fmt.Errorf("failed to get cluster image extension: %s", err)
			}

			if extension.Type == v12.AppInstaller {
				return installApplication(imageName, desiredCluster.Spec.CMD, desiredCluster.Spec.APPNames, desiredCluster.Spec.Env, extension, cf.GetConfigs(), imageEngine, applyMode)
			}

			client := utils.GetClusterClient()
			if client == nil {
				// no k8s client means to init a new cluster.
				// merge flags
				cluster, err := utils.MergeClusterWithFlags(cf.GetCluster(), &types.MergeFlags{
					Masters:    applyFlags.Masters,
					Nodes:      applyFlags.Nodes,
					CustomEnv:  applyFlags.CustomEnv,
					User:       applyFlags.User,
					Password:   applyFlags.Password,
					PkPassword: applyFlags.PkPassword,
					Pk:         applyFlags.Pk,
					Port:       applyFlags.Port,
				})

				if err != nil {
					return fmt.Errorf("failed to merge cluster with apply args: %v", err)
				}

				// set merged cluster
				cf.SetCluster(*cluster)
				return createNewCluster(imageEngine, cf, applyMode)
			}

			logrus.Infof("Start to check if need scale")

			currentCluster, err := utils.GetCurrentCluster(client)
			if err != nil {
				return errors.Wrap(err, "failed to get current cluster")
			}

			mj, md := strings.Diff(currentCluster.GetMasterIPList(), desiredCluster.GetMasterIPList())
			nj, nd := strings.Diff(currentCluster.GetNodeIPList(), desiredCluster.GetNodeIPList())
			if len(mj) == 0 && len(md) == 0 && len(nj) == 0 && len(nd) == 0 {
				logrus.Infof("No need scale, completed")
				return nil
			}

			if len(md) > 0 || len(nd) > 0 {
				logrus.Warnf("scale down not supported: %v, %v, skip them", md, nd)
			}
			if len(md) > 0 {
				return fmt.Errorf("make sure all masters' ip exist in your clusterfile: %s", applyFlags.ClusterFile)
			}

			infraDriver, err := infradriver.NewInfraDriver(&desiredCluster)
			if err != nil {
				return err
			}

			return scaleUpCluster(imageName, mj, nj, infraDriver, imageEngine, cf)
		},
	}

	applyFlags = &types.ApplyFlags{}
	applyCmd.Flags().BoolVar(&applyFlags.ForceDelete, "force", false, "force to delete the specified cluster if set true")
	applyCmd.Flags().StringVarP(&applyFlags.ClusterFile, "Clusterfile", "f", "", "Clusterfile path to apply a Kubernetes cluster")
	applyCmd.Flags().StringVarP(&applyFlags.Mode, "applyMode", "m", common.ApplyModeApply, "load images to the specified registry in advance")
	applyCmd.Flags().StringSliceVarP(&applyFlags.CustomEnv, "env", "e", []string{}, "set custom environment variables")
	// support merge clusterfile and flags, such as host ip and host auth info.
	applyCmd.Flags().StringVar(&applyFlags.Masters, "masters", "", "set count or IPList to masters")
	applyCmd.Flags().StringVar(&applyFlags.Nodes, "nodes", "", "set count or IPList to nodes")
	applyCmd.Flags().StringVarP(&applyFlags.User, "user", "u", "root", "set baremetal server username")
	applyCmd.Flags().StringVarP(&applyFlags.Password, "passwd", "p", "", "set cloud provider or baremetal server password")
	applyCmd.Flags().Uint16Var(&applyFlags.Port, "port", 22, "set the sshd service port number for the server (default port: 22)")
	applyCmd.Flags().StringVar(&applyFlags.Pk, "pk", filepath.Join(common.GetHomeDir(), ".ssh", "id_rsa"), "set baremetal server private key")
	applyCmd.Flags().StringVar(&applyFlags.PkPassword, "pk-passwd", "", "set baremetal server private key password")

	return applyCmd
}
