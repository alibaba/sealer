package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/alibaba/sealer/utils"

	"github.com/pkg/errors"

	"github.com/alibaba/sealer/logger"

	"github.com/alibaba/sealer/common"
	"github.com/alibaba/sealer/image"
	imageUtils "github.com/alibaba/sealer/image/utils"

	v1 "github.com/alibaba/sealer/types/api/v1"
	"github.com/alibaba/sealer/utils/mount"
	"github.com/alibaba/sealer/utils/ssh"
)

const (
	RemoteChmod = "cd %s  && chmod +x scripts/* && cd scripts && sh init.sh"
)

type Interface interface {
	MountRootfs(cluster *v1.Cluster, hosts []string) error
	UnMountRootfs(cluster *v1.Cluster) error
	MountImage(cluster *v1.Cluster) error
	UnMountImage(cluster *v1.Cluster) error
	Clean(cluster *v1.Cluster) error
}

type FileSystem struct {
}

func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

func (c *FileSystem) Clean(cluster *v1.Cluster) error {
	return utils.CleanFiles(common.DefaultClusterBaseDir(cluster.Name))
}

func (c *FileSystem) umountImage(cluster *v1.Cluster) error {
	mountdir := common.DefaultMountCloudImageDir(cluster.Name)
	if utils.IsFileExist(mountdir) {
		logger.Debug("unmount cluster dir %s", mountdir)
		if err := mount.NewMountDriver().Unmount(mountdir); err != nil {
			logger.Warn("failed to unmount %s, err: %v", mountdir, err)
		}
	}
	utils.CleanDir(mountdir)
	return nil
}

func (c *FileSystem) mountImage(cluster *v1.Cluster) error {
	mountdir := common.DefaultMountCloudImageDir(cluster.Name)
	if IsDir(mountdir) {
		logger.Info("image already mounted")
		return nil
	}
	//get layers
	Image, err := imageUtils.GetImage(cluster.Spec.Image)
	if err != nil {
		return err
	}
	logger.Info("image name is %s", Image.Name)
	layers, err := image.GetImageLayerDirs(Image)
	if err != nil {
		return fmt.Errorf("get layers failed: %v", err)
	}
	driver := mount.NewMountDriver()
	upperDir := filepath.Join(mountdir, "upper")
	if err = os.MkdirAll(upperDir, 0744); err != nil {
		return fmt.Errorf("create upperdir failed, %s", err)
	}
	if err = driver.Mount(mountdir, upperDir, layers...); err != nil {
		return fmt.Errorf("mount files failed %v", err)
	}
	return nil
}

func (c *FileSystem) MountImage(cluster *v1.Cluster) error {
	err := c.mountImage(cluster)
	if err != nil {
		return err
	}
	return nil
}

func (c *FileSystem) UnMountImage(cluster *v1.Cluster) error {
	err := c.umountImage(cluster)
	if err != nil {
		return err
	}
	return nil
}

func (c *FileSystem) MountRootfs(cluster *v1.Cluster, hosts []string) error {
	clusterRootfsDir := common.DefaultTheClusterRootfsDir(cluster.Name)
	//scp roofs to all Masters and Nodes,then do init.sh
	if err := mountRootfs(hosts, clusterRootfsDir, cluster); err != nil {
		return fmt.Errorf("mount rootfs failed %v", err)
	}
	return nil
}

func (c *FileSystem) UnMountRootfs(cluster *v1.Cluster) error {
	//do clean.sh,then remove all Masters and Nodes roofs
	IPList := append(cluster.Spec.Masters.IPList, cluster.Spec.Nodes.IPList...)
	if err := unmountRootfs(IPList, cluster); err != nil {
		return err
	}
	return nil
}

func mountRootfs(ipList []string, target string, cluster *v1.Cluster) error {
	SSH := ssh.NewSSHByCluster(cluster)
	if err := ssh.WaitSSHReady(SSH, ipList...); err != nil {
		return errors.Wrap(err, "check for node ssh service time out")
	}
	var wg sync.WaitGroup
	var flag bool
	var mutex sync.Mutex
	src := common.DefaultMountCloudImageDir(cluster.Name)
	// TODO scp sdk has change file mod bug
	initCmd := fmt.Sprintf(RemoteChmod, target)
	for _, ip := range ipList {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			err := SSH.Copy(ip, src, target)
			if err != nil {
				logger.Error("copy rootfs failed %v", err)
				mutex.Lock()
				flag = true
				mutex.Unlock()
			}
			err = SSH.CmdAsync(ip, initCmd)
			if err != nil {
				logger.Error("exec init.sh failed %v", err)
				mutex.Lock()
				flag = true
				mutex.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	if flag {
		return fmt.Errorf("mountRootfs failed")
	}
	return nil
}

func unmountRootfs(ipList []string, cluster *v1.Cluster) error {
	SSH := ssh.NewSSHByCluster(cluster)
	var wg sync.WaitGroup
	var flag bool
	var mutex sync.Mutex
	clusterRootfsDir := common.DefaultTheClusterRootfsDir(cluster.Name)
	execClean := fmt.Sprintf("/bin/sh -c "+common.DefaultClusterClearFile, cluster.Name)
	rmRootfs := fmt.Sprintf("rm -rf %s", clusterRootfsDir)
	for _, ip := range ipList {
		wg.Add(1)
		go func(IP string) {
			defer wg.Done()
			if err := SSH.CmdAsync(IP, execClean, rmRootfs); err != nil {
				logger.Error("%s:exec %s failed, %s", IP, execClean, err)
				mutex.Lock()
				flag = true
				mutex.Unlock()
				return
			}
		}(ip)
	}
	wg.Wait()
	if flag {
		return fmt.Errorf("unmountRootfs failed")
	}
	return nil
}

func NewFilesystem() Interface {
	return &FileSystem{}
}
