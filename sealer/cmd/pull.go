/*
Copyright © 2021 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"github.com/alibaba/sealer/image"
	"github.com/alibaba/sealer/logger"

	"os"

	"github.com/spf13/cobra"
)

// pullCmd represents the pull command
var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "pull cloud image to local",
	Long:  `sealer pull registry.cn-qingdao.aliyuncs.com/sealer/cloudrootfs:v1.16.9-alpha.5`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			logger.Error("enter the imageName")
			os.Exit(1)
		}
		if err := image.NewImageService().Pull(args[0]); err != nil {
			logger.Error(err)
			os.Exit(1)
		}
		logger.Info("Pull %s success", args[0])
	},
}

func init() {
	rootCmd.AddCommand(pullCmd)
}
