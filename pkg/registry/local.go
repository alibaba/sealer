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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sealerio/sealer/common"
	containerruntime "github.com/sealerio/sealer/pkg/container-runtime"
	"github.com/sealerio/sealer/pkg/imagedistributor"
	"github.com/sealerio/sealer/pkg/infradriver"
	"github.com/sealerio/sealer/pkg/ipvs"
	v2 "github.com/sealerio/sealer/types/api/v2"
	netutils "github.com/sealerio/sealer/utils/net"
	osutils "github.com/sealerio/sealer/utils/os"
	"github.com/sealerio/sealer/utils/shellcommand"

	"github.com/containers/common/pkg/auth"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	k8snet "k8s.io/utils/net"
)

const (
	LvscarePodFileName = "reg-lvscare.yaml"
)

type localConfigurator struct {
	*v2.LocalRegistry
	deployHosts          []net.IP
	containerRuntimeInfo containerruntime.Info
	infraDriver          infradriver.InfraDriver
	distributor          imagedistributor.Distributor
}

func (c *localConfigurator) GetRegistryInfo() RegistryInfo {
	registryInfo := RegistryInfo{Local: LocalRegistryInfo{LocalRegistry: c.LocalRegistry}}
	if *c.LocalRegistry.HA {
		registryInfo.Local.Vip = GetRegistryVIP(c.infraDriver)
		registryInfo.Local.DeployHosts = c.deployHosts
	} else {
		registryInfo.Local.DeployHosts = append(registryInfo.Local.DeployHosts, c.deployHosts[0])
	}
	return registryInfo
}

func (c *localConfigurator) GetDriver() (Driver, error) {
	endpoint := net.JoinHostPort(c.Domain, strconv.Itoa(c.Port))
	dataDir := filepath.Join(c.infraDriver.GetClusterRootfsPath(), "registry")
	return newLocalRegistryDriver(endpoint, dataDir, c.deployHosts, c.distributor), nil
}

func (c *localConfigurator) UninstallFrom(deletedMasters, deletedNodes []net.IP) error {
	// remove all registry configs on /etc/hosts
	all := append(deletedMasters, deletedNodes...)
	if err := c.removeRegistryConfig(all); err != nil {
		return err
	}
	if !*c.HA {
		return nil
	}
	// if current deployHosts is null,means clean all, just return.
	if len(c.deployHosts) == 0 {
		return nil
	}
	// if deletedMasters is nil, means no need to flush workers, just return
	if len(deletedMasters) == 0 {
		return nil
	}
	// flush ipvs policy on remain nodes
	return c.configureLvs(c.deployHosts, netutils.RemoveIPs(c.infraDriver.GetHostIPListByRole(common.NODE), deletedNodes))
}

func (c *localConfigurator) removeRegistryConfig(hosts []net.IP) error {
	var uninstallCmd []string
	if c.RegistryConfig.Username != "" && c.RegistryConfig.Password != "" {
		//todo use sdk to logout instead of shell cmd
		logoutCmd := fmt.Sprintf("docker logout %s ", net.JoinHostPort(c.Domain, strconv.Itoa(c.Port)))
		if c.containerRuntimeInfo.Type != common.Docker {
			logoutCmd = fmt.Sprintf("nerdctl logout %s ", net.JoinHostPort(c.Domain, strconv.Itoa(c.Port)))
		}
		uninstallCmd = append(uninstallCmd, logoutCmd)
	}

	f := func(host net.IP) error {
		err := c.infraDriver.CmdAsync(host, nil, strings.Join(uninstallCmd, "&&"))
		if err != nil {
			return fmt.Errorf("failed to delete registry configuration: %v", err)
		}
		return nil
	}

	return c.infraDriver.Execute(hosts, f)
}

func (c *localConfigurator) InstallOn(masters, nodes []net.IP) error {
	hosts := append(masters, nodes...)
	logrus.Infof("will install local private registry configuration on %+v\n", hosts)
	if err := c.configureRegistryNetwork(masters, nodes); err != nil {
		return err
	}

	if err := c.configureRegistryCert(hosts); err != nil {
		return err
	}

	if err := c.configureDaemonService(hosts); err != nil {
		return err
	}

	// if err := c.configureAccessCredential(hosts); err != nil {
	// 	return err
	// }

	return nil
}

// add registry domain and ip to "/etc/hosts"
// add registry ip to ipvs policy
func (c *localConfigurator) configureRegistryNetwork(masters, nodes []net.IP) error {
	if !*c.HA {
		return c.configureSingletonHostsFile(append(masters, nodes...))
	}

	eg, _ := errgroup.WithContext(context.Background())

	for i := range masters {
		master := masters[i]
		eg.Go(func() error {
			cmd := shellcommand.CommandSetHostAlias(c.Domain, master.String())
			if err := c.infraDriver.CmdAsync(master, nil, cmd); err != nil {
				return fmt.Errorf("failed to config masters hosts file: %v", err)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	// if masters is nil, means no need to flush old nodes
	if len(masters) == 0 {
		return c.configureLvs(c.deployHosts, nodes)
	}
	return c.configureLvs(c.deployHosts, c.infraDriver.GetHostIPListByRole(common.NODE))
}

func (c *localConfigurator) configureLvs(registryHosts, clientHosts []net.IP) error {
	var rs []string
	var realEndpoints []string
	hosts := netutils.IPsToIPStrs(registryHosts)
	sort.Strings(hosts)
	for _, m := range hosts {
		ep := net.JoinHostPort(m, strconv.Itoa(c.Port))
		rs = append(rs, fmt.Sprintf("--rs %s", ep))
		realEndpoints = append(realEndpoints, ep)
	}

	//todo should make lvs image name as const value in sealer repo.
	lvsImageURL := common.LvsCareRepoAndTag

	vip := GetRegistryVIP(c.infraDriver)

	vs := net.JoinHostPort(vip, strconv.Itoa(c.Port))
	// due to registry server do not have health path to check, choose "/" as default.
	healthPath := "/"
	healthSchem := "https"
	if *c.Insecure {
		healthSchem = "http"
	}

	y, err := ipvs.LvsStaticPodYaml(common.RegLvsCareStaticPodName, vs, realEndpoints, lvsImageURL, healthPath, healthSchem)
	if err != nil {
		return err
	}

	lvscareStaticCmd := ipvs.GetCreateLvscareStaticPodCmd(y, LvscarePodFileName)

	ipvsCmd := fmt.Sprintf("seautil ipvs --vs %s %s --health-path %s --health-schem %s --run-once",
		vs, strings.Join(rs, " "), healthPath, healthSchem)
	// flush all cluster nodes as latest ipvs policy.
	eg, _ := errgroup.WithContext(context.Background())

	for i := range clientHosts {
		n := clientHosts[i]
		eg.Go(func() error {
			err := c.infraDriver.CmdAsync(n, nil, ipvsCmd, lvscareStaticCmd)
			if err != nil {
				return fmt.Errorf("failed to config nodes lvs policy: %s: %v", ipvsCmd, err)
			}

			err = c.infraDriver.CmdAsync(n, nil, shellcommand.CommandSetHostAlias(c.Domain, vip))
			if err != nil {
				return fmt.Errorf("failed to config nodes hosts file cmd: %v", err)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (c *localConfigurator) configureSingletonHostsFile(hosts []net.IP) error {
	// add registry ip to "/etc/hosts"
	f := func(host net.IP) error {
		err := c.infraDriver.CmdAsync(host, nil, shellcommand.CommandSetHostAlias(c.Domain, c.deployHosts[0].String()))
		if err != nil {
			return fmt.Errorf("failed to config cluster hosts file cmd: %v", err)
		}
		return nil
	}

	return c.infraDriver.Execute(hosts, f)
}

func (c *localConfigurator) configureRegistryCert(hosts []net.IP) error {
	// if deploy registry as InsecureMode ,skip to configure cert.
	if *c.Insecure {
		return nil
	}

	var (
		endpoint = net.JoinHostPort(c.Domain, strconv.Itoa(c.Port))
		caFile   = c.Domain + ".crt"
		src      = filepath.Join(c.infraDriver.GetClusterRootfsPath(), "certs", caFile)
		dest     = filepath.Join(c.containerRuntimeInfo.CertsDir, endpoint, caFile)
	)

	return c.copy2RemoteHosts(src, dest, hosts)
}

func (c *localConfigurator) configureAccessCredential(hosts []net.IP) error {
	var (
		username        = c.RegistryConfig.Username
		password        = c.RegistryConfig.Password
		endpoint        = net.JoinHostPort(c.Domain, strconv.Itoa(c.Port))
		tmpAuthFilePath = "/tmp/config.json"
		// todo we need this config file when kubelet pull images from registry. while, we could optimize the logic here.
		remoteKubeletAuthFilePath = "/var/lib/kubelet/config.json"
	)

	if username == "" || password == "" {
		return nil
	}

	err := auth.Login(context.TODO(),
		nil,
		&auth.LoginOptions{
			AuthFile:           tmpAuthFilePath,
			Password:           password,
			Username:           username,
			Stdout:             os.Stdout,
			AcceptRepositories: true,
		},
		[]string{endpoint})

	if err != nil {
		return err
	}

	defer func() {
		err = os.Remove(tmpAuthFilePath)
		if err != nil {
			logrus.Debugf("failed to remove tmp registry auth file:%s", tmpAuthFilePath)
		}
	}()

	err = c.copy2RemoteHosts(tmpAuthFilePath, c.containerRuntimeInfo.ConfigFilePath, hosts)
	if err != nil {
		return err
	}

	err = c.copy2RemoteHosts(tmpAuthFilePath, remoteKubeletAuthFilePath, hosts)
	if err != nil {
		return err
	}

	return nil
}

func (c *localConfigurator) copy2RemoteHosts(src, dest string, hosts []net.IP) error {
	f := func(host net.IP) error {
		err := c.infraDriver.Copy(host, src, dest)
		if err != nil {
			return fmt.Errorf("failed to copy local file %s to remote %s : %v", src, dest, err)
		}
		return nil
	}

	return c.infraDriver.Execute(hosts, f)
}

func (c *localConfigurator) configureDaemonService(hosts []net.IP) error {
	var (
		src      string
		dest     string
		endpoint = net.JoinHostPort(c.Domain, strconv.Itoa(c.Port))
	)

	if endpoint == common.DefaultRegistryURL {
		return nil
	}

	if c.containerRuntimeInfo.Type == common.Docker {
		src = filepath.Join(c.infraDriver.GetClusterRootfsPath(), "etc", "daemon.json")
		dest = "/etc/docker/daemon.json"
		if err := c.configureDockerDaemonService(endpoint, src); err != nil {
			return err
		}
	}

	if c.containerRuntimeInfo.Type == common.Containerd {
		src = filepath.Join(c.infraDriver.GetClusterRootfsPath(), "etc", "hosts.toml")
		dest = filepath.Join(containerruntime.DefaultContainerdCertsDir, endpoint, "hosts.toml")
		if err := c.configureContainerdDaemonService(endpoint, src); err != nil {
			return err
		}
	}

	eg, _ := errgroup.WithContext(context.Background())

	// for docker: copy daemon.json to "/etc/docker/daemon.json"
	// for containerd : copy hosts.toml to "/etc/containerd/certs.d/${domain}:${port}/hosts.toml"
	for i := range hosts {
		ip := hosts[i]
		eg.Go(func() error {
			err := c.infraDriver.Copy(ip, src, dest)
			if err != nil {
				return err
			}

			err = c.infraDriver.CmdAsync(ip, nil, "systemctl daemon-reload")
			if err != nil {
				return err
			}
			return nil
		})
	}
	return eg.Wait()
}

func (c *localConfigurator) configureDockerDaemonService(endpoint, daemonFile string) error {
	var daemonConf DaemonConfig

	b, err := os.ReadFile(filepath.Clean(daemonFile))
	if err != nil {
		return err
	}

	b = bytes.TrimSpace(b)
	// if config file is empty, only add registry config.
	if len(b) != 0 {
		if err := json.Unmarshal(b, &daemonConf); err != nil {
			return fmt.Errorf("failed to load %s to DaemonConfig: %v", daemonFile, err)
		}
	}

	daemonConf.RegistryMirrors = append(daemonConf.RegistryMirrors, "http://"+endpoint)

	content, err := json.MarshalIndent(daemonConf, "", "  ")

	if err != nil {
		return fmt.Errorf("failed to marshal daemonFile: %v", err)
	}

	return osutils.NewCommonWriter(daemonFile).WriteFile(content)
}

func (c *localConfigurator) configureContainerdDaemonService(endpoint, hostTomlFile string) error {
	var (
		// caFile             = c.Domain + ".crt"
		// registryCaCertPath = filepath.Join(c.containerRuntimeInfo.CertsDir, endpoint, caFile)
		url = "http://" + endpoint
	)

	cfg := Hosts{
		Server: url,
		HostConfigs: map[string]HostFileConfig{
			url: {SkipServerVerify: true},
		},
	}

	bs, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal containerd hosts.toml file: %v", err)
	}

	return osutils.NewCommonWriter(hostTomlFile).WriteFile(bs)
}

type Hosts struct {
	// Server specifies the default server. When `host` is
	// also specified, those hosts are tried first.
	Server string `toml:"server"`
	// HostConfigs store the per-host configuration
	HostConfigs map[string]HostFileConfig `toml:"host"`
}

type HostFileConfig struct {
	// CACert are the public key certificates for TLS
	// Accepted types
	// - string - Single file with certificate(s)
	// - []string - Multiple files with certificates
	// CACert           interface{} `toml:"ca"`
	SkipServerVerify bool `toml:"skip_verify"`
}

type DaemonConfig struct {
	AllowNonDistributableArtifacts []string          `json:"allow-nondistributable-artifacts,omitempty"`
	APICorsHeader                  string            `json:"api-cors-header,omitempty"`
	AuthorizationPlugins           []string          `json:"authorization-plugins,omitempty"`
	Bip                            string            `json:"bip,omitempty"`
	Bridge                         string            `json:"bridge,omitempty"`
	CgroupParent                   string            `json:"cgroup-parent,omitempty"`
	ClusterAdvertise               string            `json:"cluster-advertise,omitempty"`
	ClusterStore                   string            `json:"cluster-store,omitempty"`
	Containerd                     string            `json:"containerd,omitempty"`
	ContainerdNamespace            string            `json:"containerd-namespace,omitempty"`
	ContainerdPluginNamespace      string            `json:"containerd-plugin-namespace,omitempty"`
	DataRoot                       string            `json:"data-root,omitempty"`
	Debug                          bool              `json:"debug,omitempty"`
	DefaultCgroupnsMode            string            `json:"default-cgroupns-mode,omitempty"`
	DefaultGateway                 string            `json:"default-gateway,omitempty"`
	DefaultGatewayV6               string            `json:"default-gateway-v6,omitempty"`
	DefaultRuntime                 string            `json:"default-runtime,omitempty"`
	DefaultShmSize                 string            `json:"default-shm-size,omitempty"`
	DNS                            []string          `json:"dns,omitempty"`
	DNSOpts                        []string          `json:"dns-opts,omitempty"`
	DNSSearch                      []string          `json:"dns-search,omitempty"`
	ExecOpts                       []string          `json:"exec-opts,omitempty"`
	ExecRoot                       string            `json:"exec-root,omitempty"`
	Experimental                   bool              `json:"experimental,omitempty"`
	FixedCidr                      string            `json:"fixed-cidr,omitempty"`
	FixedCidrV6                    string            `json:"fixed-cidr-v6,omitempty"`
	Group                          string            `json:"group,omitempty"`
	Hosts                          []string          `json:"hosts,omitempty"`
	Icc                            bool              `json:"icc,omitempty"`
	Init                           bool              `json:"init,omitempty"`
	InitPath                       string            `json:"init-path,omitempty"`
	InsecureRegistries             []string          `json:"insecure-registries,omitempty"`
	IP                             string            `json:"ip,omitempty"`
	IPForward                      bool              `json:"ip-forward,omitempty"`
	IPMasq                         bool              `json:"ip-masq,omitempty"`
	Iptables                       bool              `json:"iptables,omitempty"`
	IP6Tables                      bool              `json:"ip6tables,omitempty"`
	Ipv6                           bool              `json:"ipv6,omitempty"`
	Labels                         []string          `json:"labels,omitempty"`
	LiveRestore                    bool              `json:"live-restore,omitempty"`
	LogDriver                      string            `json:"log-driver,omitempty"`
	LogLevel                       string            `json:"log-level,omitempty"`
	MaxConcurrentDownloads         int               `json:"max-concurrent-downloads,omitempty"`
	MaxConcurrentUploads           int               `json:"max-concurrent-uploads,omitempty"`
	MaxDownloadAttempts            int               `json:"max-download-attempts,omitempty"`
	Mtu                            int               `json:"mtu,omitempty"`
	NoNewPrivileges                bool              `json:"no-new-privileges,omitempty"`
	NodeGenericResources           []string          `json:"node-generic-resources,omitempty"`
	OomScoreAdjust                 int               `json:"oom-score-adjust,omitempty"`
	Pidfile                        string            `json:"pidfile,omitempty"`
	RawLogs                        bool              `json:"raw-logs,omitempty"`
	RegistryMirrors                []string          `json:"registry-mirrors,omitempty"`
	SeccompProfile                 string            `json:"seccomp-profile,omitempty"`
	SelinuxEnabled                 bool              `json:"selinux-enabled,omitempty"`
	ShutdownTimeout                int               `json:"shutdown-timeout,omitempty"`
	StorageDriver                  string            `json:"storage-driver,omitempty"`
	StorageOpts                    []string          `json:"storage-opts,omitempty"`
	SwarmDefaultAdvertiseAddr      string            `json:"swarm-default-advertise-addr,omitempty"`
	TLS                            bool              `json:"tls,omitempty"`
	Tlscacert                      string            `json:"tlscacert,omitempty"`
	Tlscert                        string            `json:"tlscert,omitempty"`
	Tlskey                         string            `json:"tlskey,omitempty"`
	Tlsverify                      bool              `json:"tlsverify,omitempty"`
	UserlandProxy                  bool              `json:"userland-proxy,omitempty"`
	UserlandProxyPath              string            `json:"userland-proxy-path,omitempty"`
	UsernsRemap                    string            `json:"userns-remap,omitempty"`
	ClusterStoreOpts               map[string]string `json:"cluster-store-opts,omitempty"`
	LogOpts                        *DaemonLogOpts    `json:"log-opts,omitempty"`
}

type DaemonLogOpts struct {
	CacheDisabled string `json:"cache-disabled,omitempty"`
	CacheMaxFile  string `json:"cache-max-file,omitempty"`
	CacheMaxSize  string `json:"cache-max-size,omitempty"`
	CacheCompress string `json:"cache-compress,omitempty"`
	Env           string `json:"env,omitempty"`
	Labels        string `json:"labels,omitempty"`
	MaxFile       string `json:"max-file,omitempty"`
	MaxSize       string `json:"max-size,omitempty"`
}

func GetRegistryVIP(infraDriver infradriver.InfraDriver) string {
	vip := common.DefaultVIP
	if hosts := infraDriver.GetHostIPList(); len(hosts) > 0 && k8snet.IsIPv6(hosts[0]) {
		vip = common.DefaultVIPForIPv6
	}

	if ipv4, ok := infraDriver.GetClusterEnv()[common.EnvIPvsVIPForIPv4]; ok {
		vip = ipv4
	}

	if ipv6, ok := infraDriver.GetClusterEnv()[common.EnvIPvsVIPForIPv6]; ok {
		vip = ipv6
	}
	return vip
}
