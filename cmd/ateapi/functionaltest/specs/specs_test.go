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

// Package specs checks that the functional tests one directory up are
// exactly the tests specs.yaml specifies, each in its resource's file and in
// the order specs.yaml lists them.
package specs

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"
)

const (
	// testsDir holds the functional tests and their specs.
	testsDir  = ".."
	specsFile = "specs.yaml"
)

type specsDoc struct {
	Tests []spec `json:"tests"`
}

// spec specifies one test.
type spec struct {
	Name        string `json:"name"`
	Resource    string `json:"resource"`
	Description string `json:"description"`
}

// testFunc is a test function and the file that declares it.
type testFunc struct {
	File string // e.g. atespaces_test.go
	Name string // e.g. TestCreateAtespace
}

func TestSpecs(t *testing.T) {
	specs, err := readSpecs()
	if err != nil {
		t.Fatal(err)
	}

	// want lists the test functions the specs ask for, in their order.
	var want []testFunc
	listed := map[string]bool{}
	for i, tc := range specs {
		if err := validateSpec(tc); err != nil {
			t.Errorf("%s: test %d, %q: %v", specsFile, i, tc.Name, err)
		}
		if listed[tc.Name] {
			t.Errorf("%s: test %q is listed more than once", specsFile, tc.Name)
		}
		listed[tc.Name] = true
		want = append(want, testFunc{File: tc.Resource + "_test.go", Name: "Test" + tc.Name})
	}

	tests, err := readTests()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, tests); diff != "" {
		t.Errorf("test functions differ from %s (-%s +code):\n%s", specsFile, specsFile, diff)
	}
}

// validateSpec reports why tc names no test go test would run, or leaves out
// its resource or description.
func validateSpec(tc spec) error {
	var errs []error
	// go test runs Test<name> only if name starts with an uppercase letter.
	if r, _ := utf8.DecodeRuneInString(tc.Name); !unicode.IsUpper(r) {
		errs = append(errs, errors.New("name must start with an uppercase letter"))
	}
	// Go ignores the file of an empty resource, _test.go.
	if tc.Resource == "" {
		errs = append(errs, errors.New("no resource"))
	}
	if strings.TrimSpace(tc.Description) == "" {
		errs = append(errs, errors.New("no description"))
	}
	return errors.Join(errs...)
}

// readSpecs parses specsFile, rejecting fields the schema does not have.
func readSpecs() ([]spec, error) {
	data, err := os.ReadFile(filepath.Join(testsDir, specsFile))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", specsFile, err)
	}
	var f specsDoc
	if err := yaml.UnmarshalStrict(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", specsFile, err)
	}
	return f.Tests, nil
}

// readTests lists the test functions in testsDir, other than TestMain, by
// file name and, within a file, in declaration order.
func readTests() ([]testFunc, error) {
	paths, err := filepath.Glob(filepath.Join(testsDir, "*_test.go"))
	if err != nil {
		return nil, fmt.Errorf("listing test files: %w", err)
	}
	var declared []testFunc
	for _, path := range paths {
		funcs, err := parseTests(path)
		if err != nil {
			return nil, err
		}
		declared = append(declared, funcs...)
	}
	return declared, nil
}

// parseTests lists the test functions the file at path declares, other
// than TestMain, in declaration order.
func parseTests(path string) ([]testFunc, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var funcs []testFunc
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
			continue
		}
		funcs = append(funcs, testFunc{File: filepath.Base(path), Name: fn.Name.Name})
	}
	return funcs, nil
}
