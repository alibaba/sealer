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

package parser

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	parse2 "github.com/containers/buildah/pkg/parse"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
	"github.com/sealerio/sealer/build/kubefile/command"
	"github.com/sealerio/sealer/pkg/define/application/version"
	"github.com/sealerio/sealer/pkg/define/options"
	"github.com/sealerio/sealer/pkg/imageengine"
	"github.com/sirupsen/logrus"
)

// LegacyContext stores legacy information during the process of parsing.
// After the parsing ends, return the context to caller, and let the caller
// decide to clean.
type LegacyContext struct {
	files       []string
	directories []string
	// this is a map for appname to related-files
	// only used in test.
	apps2Files map[string][]string
}

type KubefileResult struct {
	// convert Kubefile to Dockerfile content line by line.
	Dockerfile string

	// RawCmds to launch sealer image
	// CMDS ["kubectl apply -f recommended.yaml"]
	RawCmds []string

	// LaunchedAppNames APP name list
	// LAUNCH ["myapp1","myapp2"]
	LaunchedAppNames []string

	// GlobalEnv is a set of key value pair.
	// set to sealer image some default parameters which is in global level.
	// user could overwrite it through v2.ClusterSpec at run stage.
	GlobalEnv map[string]string

	// AppEnv is a set of key value pair.
	// it is app level, only this app will be aware of its existence,
	// it is used to render app files, or as an environment variable for app startup and deletion commands
	// it takes precedence over GlobalEnv.
	AppEnvMap map[string]map[string]string

	// Applications structured APP instruction and register it to this map
	// APP myapp local://app.yaml
	Applications map[string]version.VersionedApplication

	// AppCmdsMap structured APPCMDS instruction and register it to this map
	// APPCMDS myapp ["kubectl apply -f app.yaml"]
	AppCmdsMap map[string][]string

	legacyContext LegacyContext
}

type KubefileParser struct {
	appRootPathFunc func(name string) string
	// path to build context
	buildContext string
	platform     string
	pullPolicy   string
	imageEngine  imageengine.Interface
}

func (kp *KubefileParser) ParseKubefile(rwc io.Reader, skipTLSVerify bool) (*KubefileResult, error) {
	result, err := parse(rwc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dockerfile: %v", err)
	}

	mainNode := result.AST
	return kp.generateResult(mainNode, skipTLSVerify)
}

func (kp *KubefileParser) generateResult(mainNode *Node, skipTLSVerify bool) (*KubefileResult, error) {
	var (
		result = &KubefileResult{
			Applications: map[string]version.VersionedApplication{},
			AppCmdsMap:   map[string][]string{},
			GlobalEnv:    map[string]string{},
			AppEnvMap:    map[string]map[string]string{},
			legacyContext: LegacyContext{
				files:       []string{},
				directories: []string{},
				apps2Files:  map[string][]string{},
			},
			RawCmds:          []string{},
			LaunchedAppNames: []string{},
		}

		err error

		launchCnt = 0
		cmdsCnt   = 0
		cmdCnt    = 0
	)

	defer func() {
		if err != nil {
			if err2 := result.CleanLegacyContext(); err2 != nil {
				logrus.Warn(err2)
			}
		}
	}()

	// pre-action for commands
	// for FROM, it will try to pull the image, and get apps from "FROM" image
	// for LAUNCH, it will check if it's the last line
	for i, node := range mainNode.Children {
		_command := node.Value
		if _, ok := command.SupportedCommands[_command]; !ok {
			return nil, errors.Errorf("command %s is not supported", _command)
		}

		switch _command {
		case command.From:
			// process FROM aims to pull the image, and merge the applications from
			// the FROM image.
			if err = kp.processFrom(node, result, skipTLSVerify); err != nil {
				return nil, fmt.Errorf("failed to process from: %v", err)
			}
		case command.Launch:
			launchCnt++
			if launchCnt > 1 {
				return nil, errors.New("only one launch could be specified")
			}
			if i != len(mainNode.Children)-1 {
				return nil, errors.New("launch should be the last instruction")
			}
		case command.Cmds:
			cmdsCnt++
			if cmdsCnt > 1 {
				return nil, errors.New("only one cmds could be specified")
			}
			if i != len(mainNode.Children)-1 {
				return nil, errors.New("cmds should be the last instruction")
			}

		case command.Cmd:
			cmdCnt++
			if cmdCnt > 1 {
				break
			}
			logrus.Warn("CMD is about to be deprecated.")
		}

		if cmdCnt >= 1 && launchCnt == 1 {
			return nil, errors.New("cmd and launch are mutually exclusive")
		}

		if err = kp.processOnCmd(result, node); err != nil {
			return nil, err
		}
	}

	// check result validation
	// if no app type detected and no AppCmds exist for this app, will return error.
	for name, registered := range result.Applications {
		if registered.Type() != "" {
			continue
		}

		if _, ok := result.AppCmdsMap[name]; !ok {
			return nil, fmt.Errorf("app %s need to specify APPCMDS if no app type detected", name)
		}
	}

	// register app with app env list.
	for appName, appEnv := range result.AppEnvMap {
		app := result.Applications[appName]
		app.SetEnv(appEnv)
		result.Applications[appName] = app
	}

	// register app with app cmds.
	for appName, appCmds := range result.AppCmdsMap {
		app := result.Applications[appName]
		app.SetCmds(appCmds)
		result.Applications[appName] = app
	}

	return result, nil
}

func (kp *KubefileParser) processOnCmd(result *KubefileResult, node *Node) error {
	cmd := node.Value
	switch cmd {
	case command.Label, command.Maintainer, command.Add, command.Arg, command.From, command.Run:
		result.Dockerfile = mergeLines(result.Dockerfile, node.Original)
		return nil
	case command.Env:
		// update global env to dockerfile at the same,	for using it at build stage.
		result.Dockerfile = mergeLines(result.Dockerfile, node.Original)
		return kp.processGlobalEnv(node, result)
	case command.AppEnv:
		return kp.processAppEnv(node, result)
	case command.App:
		_, err := kp.processApp(node, result)
		return err
	case command.AppCmds:
		return kp.processAppCmds(node, result)
	case command.CNI:
		return kp.processCNI(node, result)
	case command.CSI:
		return kp.processCSI(node, result)
	case command.KUBEVERSION:
		return kp.processKubeVersion(node, result)
	case command.Launch:
		return kp.processLaunch(node, result)
	case command.Cmds:
		return kp.processCmds(node, result)
	case command.Copy:
		return kp.processCopy(node, result)
	case command.Cmd:
		return kp.processCmd(node, result)
	default:
		return fmt.Errorf("failed to recognize cmd: %s", cmd)
	}
}

func (kp *KubefileParser) processCNI(node *Node, result *KubefileResult) error {
	app, err := kp.processApp(node, result)
	if err != nil {
		return err
	}
	dockerFileInstruction := fmt.Sprintf(`LABEL %s%s="true"`, command.LabelKubeCNIPrefix, app.Name())
	result.Dockerfile = mergeLines(result.Dockerfile, dockerFileInstruction)
	return nil
}

func (kp *KubefileParser) processCSI(node *Node, result *KubefileResult) error {
	app, err := kp.processApp(node, result)
	if err != nil {
		return err
	}
	dockerFileInstruction := fmt.Sprintf(`LABEL %s%s="true"`, command.LabelKubeCSIPrefix, app.Name())
	result.Dockerfile = mergeLines(result.Dockerfile, dockerFileInstruction)
	return nil
}

func (kp *KubefileParser) processKubeVersion(node *Node, result *KubefileResult) error {
	kubeVersionValue := node.Next.Value
	dockerFileInstruction := fmt.Sprintf(`LABEL %s=%s`, command.LabelSupportedKubeVersionAlpha, strconv.Quote(kubeVersionValue))
	result.Dockerfile = mergeLines(result.Dockerfile, dockerFileInstruction)
	return nil
}

func (kp *KubefileParser) processCopy(node *Node, result *KubefileResult) error {
	if node.Next == nil || node.Next.Next == nil {
		return fmt.Errorf("line %d: invalid copy instruction: %s", node.StartLine, node.Original)
	}

	copySrc := node.Next.Value
	copyDest := node.Next.Next.Value
	// support ${arch} on Kubefile COPY instruction
	// For example:
	// if arch is amd64
	// `COPY ${ARCH}/* .` will be mutated to `COPY amd64/* .`
	// `COPY $ARCH/* .` will be mutated to `COPY amd64/* .`
	_, arch, _, err := parse2.Platform(kp.platform)
	if err != nil {
		return fmt.Errorf("failed to parse platform: %v", err)
	}

	ex := shell.NewLex('\\')
	src, err := ex.ProcessWordWithMap(copySrc, map[string]string{"ARCH": arch})
	if err != nil {
		return fmt.Errorf("failed to render COPY instruction: %v", err)
	}

	tmpLine := strings.Join(append([]string{command.Copy}, src, copyDest), " ")
	result.Dockerfile = mergeLines(result.Dockerfile, tmpLine)

	return nil
}

func (kp *KubefileParser) processAppCmds(node *Node, result *KubefileResult) error {
	appNode := node.Next
	appName := appNode.Value

	if appName == "" {
		return errors.New("app name should be specified in the APPCMDS instruction")
	}

	tmpPrefix := fmt.Sprintf("%s %s", strings.TrimSpace(strings.ToUpper(command.AppCmds)), strings.TrimSpace(appName))
	appCmdsStr := strings.TrimSpace(strings.TrimPrefix(node.Original, tmpPrefix))

	var appCmds []string
	if err := json.Unmarshal([]byte(appCmdsStr), &appCmds); err != nil {
		return errors.Wrapf(err, `the APPCMDS value should be format: APPCMDS appName ["executable","param1","param2","..."]`)
	}

	// check whether the app name exist
	var appExisted bool
	for existAppName := range result.Applications {
		if existAppName == appName {
			appExisted = true
		}
	}
	if !appExisted {
		return fmt.Errorf("the specified app name(%s) for `APPCMDS` should be exist", appName)
	}

	result.AppCmdsMap[appName] = appCmds
	return nil
}

func (kp *KubefileParser) processAppEnv(node *Node, result *KubefileResult) error {
	var (
		appName = ""
		envList []string
	)

	// first node value is the command
	for ptr := node.Next; ptr != nil; ptr = ptr.Next {
		val := ptr.Value
		// record the first word to be the app name
		if appName == "" {
			appName = val
			continue
		}
		envList = append(envList, val)
	}

	if appName == "" {
		return errors.New("app name should be specified in the APPENV instruction")
	}

	if _, ok := result.Applications[appName]; !ok {
		return fmt.Errorf("the specified app name(%s) for `APPENV` should be exist", appName)
	}

	tmpEnv := make(map[string]string)
	for _, elem := range envList {
		var kv []string
		if kv = strings.SplitN(elem, "=", 2); len(kv) != 2 {
			continue
		}
		tmpEnv[kv[0]] = kv[1]
	}

	appEnv := result.AppEnvMap[appName]
	if appEnv == nil {
		appEnv = make(map[string]string)
	}

	for k, v := range tmpEnv {
		appEnv[k] = v
	}

	result.AppEnvMap[appName] = appEnv
	return nil
}

func (kp *KubefileParser) processGlobalEnv(node *Node, result *KubefileResult) error {
	valueList := strings.SplitN(node.Original, "ENV ", 2)
	if len(valueList) != 2 {
		return fmt.Errorf("line %d: invalid ENV instruction: %s", node.StartLine, node.Original)
	}
	envs := valueList[1]

	for _, elem := range strings.Split(envs, " ") {
		if elem == "" {
			continue
		}

		var kv []string
		if kv = strings.SplitN(elem, "=", 2); len(kv) != 2 {
			continue
		}
		result.GlobalEnv[kv[0]] = kv[1]
	}

	return nil
}

func (kp *KubefileParser) processCmd(node *Node, result *KubefileResult) error {
	original := node.Original
	cmd := strings.Split(original, "CMD ")
	node.Next.Value = cmd[1]
	result.RawCmds = append(result.RawCmds, node.Next.Value)
	return nil
}

func (kp *KubefileParser) processCmds(node *Node, result *KubefileResult) error {
	cmdsNode := node.Next
	for iter := cmdsNode; iter != nil; iter = iter.Next {
		result.RawCmds = append(result.RawCmds, iter.Value)
	}
	return nil
}

func (kp *KubefileParser) processLaunch(node *Node, result *KubefileResult) error {
	appNode := node.Next
	for iter := appNode; iter != nil; iter = iter.Next {
		appName := iter.Value
		appName = strings.TrimSpace(appName)
		if _, ok := result.Applications[appName]; !ok {
			return errors.Errorf("application %s does not exist in the image", appName)
		}
		result.LaunchedAppNames = append(result.LaunchedAppNames, appName)
	}

	return nil
}

func (kp *KubefileParser) processFrom(node *Node, result *KubefileResult, skipTLSVerify bool) error {
	var (
		platform  = parse2.DefaultPlatform()
		flags     = node.Flags
		imageNode = node.Next
	)
	if len(flags) > 0 {
		f, err := parseListFlag(flags[0])
		if err != nil {
			return err
		}
		if f.flag != "platform" {
			return errors.Errorf("flag %s is not available in FROM", f.flag)
		}
		platform = f.items[0]
	}

	if imageNode == nil || len(imageNode.Value) == 0 {
		return errors.Errorf("image should be specified in the FROM")
	}
	image := imageNode.Value
	if image == "scratch" {
		return nil
	}

	id, err := kp.imageEngine.Pull(&options.PullOptions{
		PullPolicy:    kp.pullPolicy,
		Image:         image,
		Platform:      platform,
		SkipTLSVerify: skipTLSVerify,
	})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %v", image, err)
	}

	imageSpec, err := kp.imageEngine.Inspect(&options.InspectOptions{ImageNameOrID: id})
	if err != nil {
		return fmt.Errorf("failed to get image-extension %s: %s", image, err)
	}

	for _, app := range imageSpec.ImageExtension.Applications {
		// for range has problem.
		// can't assign address to the target.
		// we should use temp value.
		// https://github.com/golang/gofrontend/blob/e387439bfd24d5e142874b8e68e7039f74c744d7/go/statements.cc#L5501
		theApp := app
		result.Applications[app.Name()] = theApp
	}

	return nil
}

func (kr *KubefileResult) CleanLegacyContext() error {
	var (
		lc  = kr.legacyContext
		err error
	)

	for _, f := range lc.files {
		err = os.Remove(f)
	}

	for _, dir := range lc.directories {
		err = os.RemoveAll(dir)
	}

	return errors.Wrap(err, "failed to clean legacy context")
}

func NewParser(appRootPath string,
	buildOptions options.BuildOptions,
	imageEngine imageengine.Interface,
	platform string) *KubefileParser {
	return &KubefileParser{
		// application will be put under approot/name/
		appRootPathFunc: func(name string) string {
			return makeItDir(filepath.Join(appRootPath, name))
		},
		imageEngine:  imageEngine,
		buildContext: buildOptions.ContextDir,
		pullPolicy:   buildOptions.PullPolicy,
		platform:     platform,
	}
}
