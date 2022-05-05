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

package utils

import (
	"crypto/md5" // #nosec
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func MD5(body []byte) string {
	bytes := md5.Sum(body) // #nosec
	return hex.EncodeToString(bytes[:])
}

//FileMD5 count file md5
func FileMD5(path string) (string, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}

	m := md5.New() // #nosec
	if _, err := io.Copy(m, file); err != nil {
		return "", err
	}

	fileMd5 := fmt.Sprintf("%x", m.Sum(nil))
	return fileMd5, nil
}
