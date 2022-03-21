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

package image

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/alibaba/sealer/pkg/image/types"

	"sigs.k8s.io/yaml"

	"github.com/alibaba/sealer/common"
	"github.com/alibaba/sealer/logger"
	"github.com/alibaba/sealer/pkg/image/store"
	v1 "github.com/alibaba/sealer/types/api/v1"
	"github.com/alibaba/sealer/utils"
	"github.com/alibaba/sealer/utils/archive"
)

type DefaultImageFileService struct {
	layerStore store.LayerStore
	imageStore store.ImageStore
}

func (d DefaultImageFileService) Load(imageSrc string) error {
	var (
		srcFile          *os.File
		size             int64
		err              error
		repoFile         = filepath.Join(common.DefaultLayerDir, common.DefaultMetadataName)
		imageMetadataMap store.ImageMetadataMap
	)

	srcFile, err = os.Open(filepath.Clean(imageSrc))
	if err != nil {
		return fmt.Errorf("failed to open %s, err : %v", imageSrc, err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			logger.Error("failed to close file")
		}
	}()

	srcFi, err := srcFile.Stat()
	if err != nil {
		return err
	}
	size = srcFi.Size()

	if _, err = archive.Decompress(srcFile, common.DefaultLayerDir, archive.Options{Compress: false}); err != nil {
		return err
	}

	repoBytes, err := ioutil.ReadFile(filepath.Clean(repoFile))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(repoBytes, &imageMetadataMap); err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(repoFile); err != nil {
			logger.Error("failed to close file")
		}
	}()

	for name, repo := range imageMetadataMap {
		for _, m := range repo.Manifests {
			var image v1.Image
			imageTempFile := filepath.Join(common.DefaultLayerDir, m.ID+".yaml")
			if err = utils.UnmarshalYamlFile(imageTempFile, &image); err != nil {
				return fmt.Errorf("failed to parsing %s, err: %v", imageTempFile, err)
			}
			for _, layer := range image.Spec.Layers {
				if layer.ID == "" {
					continue
				}
				roLayer, err := store.NewROLayer(layer.ID, size, nil)
				if err != nil {
					return err
				}

				err = d.layerStore.RegisterLayerIfNotPresent(roLayer)
				if err != nil {
					return fmt.Errorf("failed to register layer, err: %v", err)
				}
			}
			err = d.imageStore.Save(image)
			if err != nil {
				return err
			}
			if err = os.Remove(imageTempFile); err != nil {
				logger.Error("failed to cleanup local temp file %s:%v", imageTempFile, err)
			}
		}
		logger.Info("load image %s successfully", name)
	}

	return nil
}

func (d DefaultImageFileService) Save(imageName, imageTar string, platforms []*v1.Platform) error {
	var (
		pathsToCompress []string
		ml              []*types.ManifestDescriptor
		repoData        = make(store.ImageMetadataMap)
	)

	meta, err := d.imageStore.GetImageMetadataMap()
	if err != nil {
		return err
	}
	manifestList, ok := meta[imageName]
	if !ok {
		return fmt.Errorf("image: %s not found", imageName)
	}

	if len(platforms) == 0 {
		for _, m := range manifestList.Manifests {
			platforms = append(platforms, &m.Platform)
		}
	}

	if err := utils.MkFileFullPathDir(imageTar); err != nil {
		return fmt.Errorf("failed to create %s, err: %v", imageTar, err)
	}
	file, err := os.Create(filepath.Clean(imageTar))
	if err != nil {
		return fmt.Errorf("failed to create %s, err: %v", imageTar, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Error("failed to close file")
		}
	}()

	tempDir, err := utils.MkTmpdir()
	if err != nil {
		return fmt.Errorf("failed to create %s, err: %v", tempDir, err)
	}
	defer utils.CleanDir(tempDir)

	repofile := filepath.Join(tempDir, common.DefaultMetadataName)
	imageStore, err := store.NewDefaultImageStore()
	if err != nil {
		return err
	}

	// write image layer file and image yaml file.
	for _, p := range platforms {
		metadata, err := imageStore.GetImageMetadataItem(imageName, p)
		if err != nil {
			return err
		}
		ml = append(ml, metadata)
		// add image layer
		ima, err := imageStore.GetByName(imageName, p)
		if err != nil {
			return err
		}
		layerDirs, err := GetImageLayerDirs(ima)
		if err != nil {
			return err
		}
		pathsToCompress = append(pathsToCompress, layerDirs...)
		// add image yaml
		imgBytes, err := yaml.Marshal(ima)
		if err != nil {
			return fmt.Errorf("failed to marchal image, err: %s", err)
		}
		imagePath := filepath.Join(tempDir, ima.Spec.ID+".yaml")
		if err = utils.AtomicWriteFile(imagePath, imgBytes, common.FileMode0644); err != nil {
			return fmt.Errorf("failed to write temp file %s, err: %v ", imagePath, err)
		}
		pathsToCompress = append(pathsToCompress, imagePath)
	}

	repoData[imageName] = &types.ManifestList{Manifests: ml}
	repoBytes, err := json.Marshal(repoData)
	if err != nil {
		return err
	}
	if err = utils.AtomicWriteFile(repofile, repoBytes, common.FileMode0644); err != nil {
		return fmt.Errorf("failed to write temp file %s, err: %v ", repofile, err)
	}
	// add image repo data
	pathsToCompress = append(pathsToCompress, repofile)
	tarReader, err := archive.TarWithRootDir(pathsToCompress...)
	if err != nil {
		return fmt.Errorf("failed to get tar reader for %s, err: %s", imageName, err)
	}
	defer func() {
		if err := tarReader.Close(); err != nil {
			logger.Error("failed to close file")
		}
	}()

	_, err = io.Copy(file, tarReader)
	return err
}

func (d DefaultImageFileService) Merge(image *v1.Image) error {
	panic("implement me")
}
