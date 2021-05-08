package cmd

import (
	"os"

	"github.com/alibaba/sealer/common"

	"github.com/alibaba/sealer/cert"

	"github.com/alibaba/sealer/apply"

	"github.com/alibaba/sealer/logger"
	"github.com/spf13/cobra"
)

var runArgs *common.RunArgs

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "run a cluster with images and arguments",
	Long: `sealer run registry.cn-qingdao.aliyuncs.com/seadent/cloudrootfs:v1.16.9-alpha.7 --masters [arg] --nodes [arg]
examples:
create default cluster:
	sealer run registry.cn-qingdao.aliyuncs.com/seadent/cloudrootfs:v1.16.9-alpha.7

create cluster by cloud provider, just set the number of masters or nodes:
	sealer run registry.cn-qingdao.aliyuncs.com/seadent/cloudrootfs:v1.16.9-alpha.7 --masters 3 --nodes 3

create cluster to your baremetal server, appoint the iplist:
	sealer run registry.cn-qingdao.aliyuncs.com/seadent/cloudrootfs:v1.16.9-alpha.7 --masters 192.168.0.2,192.168.0.3,192.168.0.4 \
		--nodes 192.168.0.5,192.168.0.6,192.168.0.7
`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			logger.Error("cluster image name not found")
			os.Exit(1)
		}
		applier, err := apply.NewApplierFromArgs(args[0], runArgs)
		if err != nil {
			logger.Error(err)
			os.Exit(1)
		}
		if err := applier.Apply(); err != nil {
			logger.Error(err)
			os.Exit(1)
		}
	},
}

func init() {
	runArgs = &common.RunArgs{}
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&runArgs.Masters, "masters", "m", "", "set Count or IPList to masters")
	runCmd.Flags().StringVarP(&runArgs.Nodes, "nodes", "n", "", "set Count or IPList to nodes")
	runCmd.Flags().StringVarP(&runArgs.User, "user", "u", "root", "set baremetal server username")
	runCmd.Flags().StringVarP(&runArgs.Password, "passwd", "p", "", "set cloud provider or baremetal server password")
	runCmd.Flags().StringVarP(&runArgs.Pk, "pk", "", cert.GetUserHomeDir()+"/.ssh/id_rsa", "set baremetal server private key")
	runCmd.Flags().StringVarP(&runArgs.PkPassword, "pk-passwd", "", "", "set baremetal server  private key password")
}
