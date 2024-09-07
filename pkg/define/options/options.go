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

package options

const (
	WithLiteMode = "lite"
	WithAllMode  = "all"
)

var SupportedBuildModes = []string{
	WithLiteMode,
	WithAllMode,
}

// BuildOptions should be out of buildah scope.
type BuildOptions struct {
	Kubefile       string
	DockerFilePath string
	ContextDir     string
	PullPolicy     string
	ImageType      string
	//Manifest          string
	Tag               string
	BuildArgs         []string
	Platforms         []string
	Labels            []string
	Annotations       []string
	NoCache           bool
	Base              bool
	ImageList         string
	ImageListWithAuth string
	IgnoredImageList  string

	//BuildMode means whether to download container image during the build process
	// default value is download all container images.
	BuildMode     string
	SkipTLSVerify bool
}

type FromOptions struct {
	Image string
	Quiet bool
}

type MountOptions struct {
	//Json bool
	Containers []string
}

type JSONMount struct {
	Container  string `json:"container,omitempty"`
	MountPoint string `json:"mountPoint"`
}

type CopyOptions struct {
	AddHistory bool
	Quiet      bool
	IgnoreFile string
	ContextDir string
	Container  string
	// paths of files relative to context dir.
	SourcesRel2CxtDir      []string
	DestinationInContainer string
}

type ConfigOptions struct {
	ContainerID string
	Annotations []string
}

type CommitOptions struct {
	Format             string
	Manifest           string
	Timestamp          int64
	Quiet              bool
	Rm                 bool
	Squash             bool
	DisableCompression bool
	ContainerID        string
	Image              string
}

type LoginOptions struct {
	Domain        string
	Username      string
	Password      string
	SkipTLSVerify bool
}

type LogoutOptions struct {
	All    bool
	Domain string
}

type PushOptions struct {
	Authfile      string
	CertDir       string
	Format        string
	Image         string
	Destination   string
	Rm            bool
	Quiet         bool
	SkipTLSVerify bool
	All           bool
}

type PullOptions struct {
	CertDir       string
	Quiet         bool
	SkipTLSVerify bool
	PullPolicy    string
	Image         string
	Platform      string
}

type ImagesOptions struct {
	All       bool
	Digests   bool
	NoHeading bool
	NoTrunc   bool
	Quiet     bool
	History   bool
	JSON      bool
}

type SaveOptions struct {
	Compress bool
	Format   string
	// don't support currently
	MultiImageArchive bool
	Output            string
	Quiet             bool
	ImageNameOrID     string
	TmpDir            string
}

type LoadOptions struct {
	Input  string
	TmpDir string
	Quiet  bool
}

type InspectOptions struct {
	Format        string
	InspectType   string
	ImageNameOrID string
}

type BuildRootfsOptions struct {
	ImageNameOrID string
	DestDir       string
}

type RemoveImageOptions struct {
	ImageNamesOrIDs []string
	Force           bool
	Prune           bool
}

type EngineGlobalConfigurations struct {
	AuthFile  string
	GraphRoot string
	RunRoot   string
}

type RemoveContainerOptions struct {
	ContainerNamesOrIDs []string
	All                 bool
}

type TagOptions struct {
	ImageNameOrID string
	Tags          []string
}

type ManifestCreateOpts struct {
}

type ManifestInspectOpts struct {
}

type ManifestDeleteOpts struct {
}

type ManifestAddOpts struct {
	Os          string
	Arch        string
	Variant     string
	OsVersion   string
	OsFeatures  []string
	Annotations []string
	All         bool
	TargetName  string
}

type ManifestRemoveOpts struct {
}
