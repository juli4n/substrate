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

// Package parser builds a comment-preserving protoreflect.FileDescriptor
// for pkg/proto/ateapipb/ateapi.proto.
//
// protoc-gen-go strips SourceCodeInfo (the doc comments attached to each
// message/field/enum) from the descriptor bytes it embeds in generated .pb.go
// files - comments only survive as literal Go source comments, which aren't
// reflectable at runtime. To recover them, this package parses ateapi.proto
// directly from source with protocompile, a pure-Go proto compiler - no
// protoc binary, no subprocess.
package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bufbuild/protocompile"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// targetProtoFile is repo-root-relative, so it and everything it imports
// resolve against the same import root (repoRoot).
const targetProtoFile = "pkg/proto/ateapipb/ateapi.proto"

// rootModule is the module declared by the repository root's go.mod - not
// this tool's own module (tools/apitool has its own go.mod, per
// docs/dev/code-layout.md, and replaces this one to reach it).
const rootModule = "module github.com/agent-substrate/substrate"

// findRepoRoot locates the repository root by walking upward from the
// working directory until it finds a go.mod whose module line matches
// rootModule - the same approach `git rev-parse --show-toplevel` takes for
// finding a repo boundary. A plain "cwd must be the root" check doesn't
// work here: apitool is invoked as `cd tools/apitool && go run .`, since
// it's a separate module, so its own working directory is never the root.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("while getting working directory: %w", err)
	}
	for {
		goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			first, _, _ := strings.Cut(string(goMod), "\n")
			if strings.TrimSpace(first) == rootModule {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find a go.mod declaring %q above %s", rootModule, mustGetwd())
		}
		dir = parent
	}
}

func mustGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "<unknown working directory>"
	}
	return dir
}

// buildFileDescriptor parses ateapi.proto - and everything it transitively
// imports, including well-known types like google/protobuf/descriptor.proto
// - directly from source under repoRoot, with doc comments attached.
func buildFileDescriptor(ctx context.Context, repoRoot string) (protoreflect.FileDescriptor, error) {
	compiler := protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			ImportPaths: []string{repoRoot},
		}),
		SourceInfoMode: protocompile.SourceInfoStandard,
	}
	files, err := compiler.Compile(ctx, targetProtoFile)
	if err != nil {
		return nil, fmt.Errorf("while compiling %s: %w", targetProtoFile, err)
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("expected exactly one compiled file for %s, got %d", targetProtoFile, len(files))
	}
	return files[0], nil
}

// Parse finds the repository root and builds ateapi.proto's FileDescriptor
// - the package's one public entry point. Returns repoRoot too, since
// callers also use it to resolve other repo-relative paths (e.g. --out).
func Parse(ctx context.Context) (repoRoot string, fd protoreflect.FileDescriptor, err error) {
	repoRoot, err = findRepoRoot()
	if err != nil {
		return "", nil, err
	}
	fd, err = buildFileDescriptor(ctx, repoRoot)
	return repoRoot, fd, err
}
