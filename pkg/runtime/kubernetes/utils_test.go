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

package kubernetes

import (
	"reflect"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestVersion_Version(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		wantVersion kubeVersion
	}{
		{
			name:        "test Version field correct",
			version:     "v1.19.8",
			wantVersion: []string{"1", "19", "8"},
		},
		{
			name:        "test Version field incorrect",
			version:     "-v1.19.8-",
			wantVersion: []string{""},
		},
		{
			name:        "test Version field blank",
			version:     "",
			wantVersion: []string{""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v kubeVersion
			v1 := v.Version(tt.version)
			if !reflect.DeepEqual(v1, tt.wantVersion) {
				t.Errorf("Version parse failed! got: %v, want: %v", v1, tt.wantVersion)
			}
		})
	}
}

func TestVersion_Compare(t *testing.T) {
	tests := []struct {
		name         string
		givenVersion kubeVersion
		oldVersion   kubeVersion
		wantRes      bool
	}{
		{
			name:         "test v > v1",
			givenVersion: []string{"1", "20", "3"},
			oldVersion:   []string{"1", "19", "8"},
			wantRes:      true,
		},
		{
			name:         "test v = v1",
			givenVersion: []string{"1", "19", "8"},
			oldVersion:   []string{"1", "19", "8"},
			wantRes:      true,
		},
		{
			name:         "test v < v1",
			givenVersion: []string{"1", "19", "8"},
			oldVersion:   []string{"1", "20", "3"},
			wantRes:      false,
		},
		{
			name:         "test1 old Version illegal",
			givenVersion: []string{"1", "19", "8"},
			oldVersion:   []string{""},
			wantRes:      false,
		},
		{
			name:         "test2 give Version illegal",
			givenVersion: []string{""},
			oldVersion:   []string{"1", "19", "8"},
			wantRes:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.givenVersion
			res, err := v.Compare(tt.oldVersion)
			if err != nil {
				logrus.Errorf("compare kubernetes version failed: %s", err)
			}
			if !reflect.DeepEqual(res, tt.wantRes) {
				t.Errorf("Version compare failed! result: %v, want: %v", res, tt.wantRes)
			}
		})
	}
}
