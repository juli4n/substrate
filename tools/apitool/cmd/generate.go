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

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/agent-substrate/substrate/tools/apitool/internal/docsite"
	"github.com/agent-substrate/substrate/tools/apitool/internal/model"
	"github.com/agent-substrate/substrate/tools/apitool/internal/parser"
)

var outPath string

// documentedService is the only service apitool documents.
const documentedService = "Control"

var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate the self-contained API reference HTML page",
	RunE:  runGenerate,
	// A schema-validation failure already prints one line per problem;
	// don't also dump usage text. main.go prints the returned error either
	// way.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	generateCmd.Flags().StringVar(&outPath, "out", "bin/api-reference.html", "output HTML file path; a relative path is resolved against the repository root, not the current directory")
	rootCmd.AddCommand(generateCmd)
}

func runGenerate(cmd *cobra.Command, args []string) error {
	repoRoot, fd, err := parser.Parse(cmd.Context())
	if err != nil {
		return fmt.Errorf("while building file descriptor: %w", err)
	}

	api := model.FilterToService(model.Build(fd), documentedService)

	if errs := model.ValidateExampleJSON(api); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(cmd.ErrOrStderr(), e)
		}
		return fmt.Errorf("%d example(s) failed schema validation - fix internal/model/examples/*.json", len(errs))
	}

	html, err := docsite.Render(api)
	if err != nil {
		return fmt.Errorf("while rendering HTML: %w", err)
	}

	out := outPath
	if !filepath.IsAbs(out) {
		out = filepath.Join(repoRoot, out)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return fmt.Errorf("while creating output directory: %w", err)
	}
	if err := os.WriteFile(out, html, 0o644); err != nil {
		return fmt.Errorf("while writing %s: %w", out, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d services, %d messages, %d enums)\n", out, len(api.Services), len(api.Messages), len(api.Enums))
	return nil
}
