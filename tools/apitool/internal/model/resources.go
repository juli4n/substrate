// Copyright 2026 Google LLC
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

package model

import (
	"fmt"
	"strings"
)

var resourceNames = []string{
	"Actor",
	"ActorSnapshot",
	"ActorSnapshotTag",
	"ActorTemplate",
	"Atespace",
	"Worker",
}

func resourceForMethodName(methodName string) (string, error) {
	var matches []string
	for _, name := range resourceNames {
		if strings.Contains(methodName, name) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no name in resourceNames is contained in %q - add one naming the resource this method operates on", methodName)
	}

	longest := matches[:1]
	for _, m := range matches[1:] {
		switch {
		case len(m) > len(longest[0]):
			longest = []string{m}
		case len(m) == len(longest[0]):
			longest = append(longest, m)
		}
	}
	if len(longest) > 1 {
		return "", fmt.Errorf("%q matches multiple equally-specific resource names %v - rename the method or the resource so only one matches", methodName, longest)
	}
	return longest[0], nil
}
