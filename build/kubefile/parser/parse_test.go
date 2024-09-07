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
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	platformParse "github.com/containers/buildah/pkg/parse"
	"github.com/stretchr/testify/assert"

	"github.com/sealerio/sealer/pkg/define/application"
	v1 "github.com/sealerio/sealer/pkg/define/application/v1"
	"github.com/sealerio/sealer/pkg/define/application/version"
	"github.com/sealerio/sealer/pkg/define/options"
)

const (
// nginxDemoURL = "https://raw.githubusercontent.com/kubernetes/website/main/content/en/examples/application/deployment.yaml"
)

var testParser *KubefileParser

var testAppRootPath = "/test/apps"

func TestParserKubeApp(t *testing.T) {
	nginxDemoPath := "./test/kube-nginx-deployment/deployment.yaml"
	imageEngine := testImageEngine{}
	opts := options.BuildOptions{
		PullPolicy: "missing",
	}

	buildCxt, err := setupTempContext()
	assert.Equal(t, nil, err)
	defer func() {
		_ = os.RemoveAll(buildCxt)
	}()

	opts.ContextDir = buildCxt
	testParser = NewParser(testAppRootPath, opts, imageEngine, platformParse.DefaultPlatform())

	var (
		app1Name = "nginx"
		app1Path = testParser.appRootPathFunc(app1Name)
		text     = fmt.Sprintf(`
FROM scratch
APP %s local://%s
LAUNCH ["%s"]
`, app1Name, nginxDemoPath, app1Name)
	)

	reader := bytes.NewReader([]byte(text))
	result, err := testParser.ParseKubefile(reader, true)
	if err != nil {
		t.Fatalf("failed to parse kubefile: %s", err)
	}
	defer func() {
		_ = result.CleanLegacyContext()
	}()

	var expectedText = fmt.Sprintf(`
FROM scratch
copy %s %s
`,
		strings.Join(result.legacyContext.apps2Files[app1Name], " "),
		app1Path,
	)

	result.Dockerfile = strings.TrimSpace(result.Dockerfile)
	expectedResult := &KubefileResult{
		Dockerfile:       strings.TrimSpace(expectedText),
		LaunchedAppNames: []string{app1Name},
		Applications: map[string]version.VersionedApplication{
			app1Name: v1.NewV1Application(
				app1Name,
				application.KubeApp,
				[]string{nginxDemoPath},
			),
		},
	}

	assert.Equal(t, expectedResult.Dockerfile, result.Dockerfile)
	assert.Equal(t, len(expectedResult.Applications), len(result.Applications))
	assert.Equal(t, expectedResult.Applications[app1Name].Name(), result.Applications[app1Name].Name())
	assert.Equal(t, expectedResult.Applications[app1Name].Type(), result.Applications[app1Name].Type())
	assert.Equal(t, expectedResult.LaunchedAppNames, result.LaunchedAppNames)
}

func TestParserHelmApp(t *testing.T) {
	githubAppPath := "./test/brigade-github-app"
	imageEngine := testImageEngine{}
	opts := options.BuildOptions{
		PullPolicy: "missing",
	}

	buildCxt, err := setupTempContext()
	assert.Equal(t, nil, err)
	defer func() {
		_ = os.RemoveAll(buildCxt)
	}()

	opts.ContextDir = buildCxt
	testParser = NewParser(testAppRootPath, opts, imageEngine, platformParse.DefaultPlatform())

	var (
		app1Name = "github-app"
		app1Path = testParser.appRootPathFunc(app1Name)
		text     = fmt.Sprintf(`
FROM scratch
APP %s local://%s
LAUNCH %s
`, app1Name, githubAppPath, app1Name)
	)

	reader := bytes.NewReader([]byte(text))
	result, err := testParser.ParseKubefile(reader, true)
	if err != nil {
		t.Fatalf("failed to parse kubefile: %s", err)
	}
	defer func() {
		_ = result.CleanLegacyContext()
	}()

	var expectedText = fmt.Sprintf(`
FROM scratch
copy %s %s
`,
		strings.Join(result.legacyContext.apps2Files[app1Name], " "),
		app1Path,
	)

	result.Dockerfile = strings.TrimSpace(result.Dockerfile)
	expectedResult := &KubefileResult{
		Dockerfile:       strings.TrimSpace(expectedText),
		LaunchedAppNames: []string{app1Name},
		Applications: map[string]version.VersionedApplication{
			app1Name: v1.NewV1Application(
				app1Name,
				application.HelmApp,
				[]string{githubAppPath},
			),
		},
	}

	assert.Equal(t, expectedResult.Dockerfile, result.Dockerfile)
	assert.Equal(t, len(expectedResult.Applications), len(result.Applications))
	assert.Equal(t, expectedResult.Applications[app1Name].Name(), result.Applications[app1Name].Name())
	assert.Equal(t, expectedResult.Applications[app1Name].Type(), result.Applications[app1Name].Type())
	assert.Equal(t, expectedResult.LaunchedAppNames, result.LaunchedAppNames)
}

func TestParserCMDS(t *testing.T) {
	imageEngine := testImageEngine{}
	opts := options.BuildOptions{
		PullPolicy: "missing",
	}

	buildCxt, err := setupTempContext()
	assert.Equal(t, nil, err)
	defer func() {
		_ = os.RemoveAll(buildCxt)
	}()

	opts.ContextDir = buildCxt
	testParser = NewParser(testAppRootPath, opts, imageEngine, platformParse.DefaultPlatform())

	var (
		text = fmt.Sprintf(`
FROM scratch
CMDS ["%s", "%s"]
`, "kubectl apply -f abc.yaml", "kubectl apply -f bcd.yaml")
	)

	reader := bytes.NewReader([]byte(text))
	result, err := testParser.ParseKubefile(reader, true)
	if err != nil {
		t.Fatalf("failed to parse kubefile: %s", err)
	}
	defer func() {
		_ = result.CleanLegacyContext()
	}()

	var expectedText = `
FROM scratch
`

	result.Dockerfile = strings.TrimSpace(result.Dockerfile)
	expectedResult := &KubefileResult{
		Dockerfile: strings.TrimSpace(expectedText),
		RawCmds: []string{
			"kubectl apply -f abc.yaml",
			"kubectl apply -f bcd.yaml",
		},
		Applications: map[string]version.VersionedApplication{},
	}

	assert.Equal(t, expectedResult.Dockerfile, result.Dockerfile)
	assert.Equal(t, len(expectedResult.Applications), len(result.Applications))
	assert.Equal(t, expectedResult.RawCmds, result.RawCmds)
}

func TestParserEnv(t *testing.T) {
	appFilePath := "./test/kube-nginx-deployment/deployment.yaml"
	imageEngine := testImageEngine{}
	opts := options.BuildOptions{
		PullPolicy: "missing",
	}

	buildCxt, err := setupTempContext()
	assert.Equal(t, nil, err)
	defer func() {
		_ = os.RemoveAll(buildCxt)
	}()

	opts.ContextDir = buildCxt
	testParser = NewParser(testAppRootPath, opts, imageEngine, platformParse.DefaultPlatform())

	var (
		text = fmt.Sprintf(`
FROM scratch 
APP app1 local://%s
ENV globalKey=globalValue
APPENV app1 key1=value1 key2=value2
APPENV app1 key1=value3 key2=value3
LAUNCH ["app1"]`, appFilePath)
	)

	reader := bytes.NewReader([]byte(text))
	result, err := testParser.ParseKubefile(reader, true)
	if err != nil {
		t.Fatalf("failed to parse kubefile: %s", err)
	}
	defer func() {
		_ = result.CleanLegacyContext()
	}()

	expectedResult := &KubefileResult{
		GlobalEnv: map[string]string{
			"globalKey": "globalValue",
		},
		AppEnvMap: map[string]map[string]string{
			"app1": {"key1": "value3", "key2": "value3"},
		},
	}

	assert.Equal(t, expectedResult.GlobalEnv, result.GlobalEnv)
	assert.Equal(t, expectedResult.AppEnvMap, result.AppEnvMap)
}

func setupTempContext() (string, error) {
	tmpDir, err := os.MkdirTemp("/tmp/", "sealer-test")
	if err != nil {
		return "", err
	}

	return tmpDir, nil
}
