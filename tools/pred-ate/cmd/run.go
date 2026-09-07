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
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/agent-substrate/substrate/tools/pred-ate/pkg/findings"
	"github.com/agent-substrate/substrate/tools/pred-ate/pkg/vm"
	"github.com/spf13/cobra"
)

var hintFlag string
var maxTokensFlag int64
var maxInputTokensFlag int64
var maxOutputTokensFlag int64

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Launch an autonomous bug-hunting session in an ephemeral VM",
	Long: `Boot an ephemeral QEMU sandbox VM, mount the Substrate repository and findings
database read-only, run the autonomous Antigravity agent to test a novel hypothesis,
and record the final finding and reproducer into the findings registry.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if globalCfg.GeminiAPIKey == "" {
			return fmt.Errorf("GEMINI_API_KEY must be provided via --gemini-api-key or environment variable")
		}

		if _, err := os.Stat(globalCfg.BaseImage); err != nil {
			return fmt.Errorf("base image '%s' not found. Run 'pred-ate init-image' or specify --base-image", globalCfg.BaseImage)
		}

		workspaceAbs, err := filepath.Abs(globalCfg.Workspace)
		if err != nil {
			return fmt.Errorf("invalid workspace directory: %w", err)
		}

		findingsAbs, err := filepath.Abs(globalCfg.FindingsDir)
		if err != nil {
			return fmt.Errorf("invalid findings directory: %w", err)
		}
		if err := os.MkdirAll(findingsAbs, 0755); err != nil {
			return fmt.Errorf("failed to create findings directory: %w", err)
		}

		runID := fmt.Sprintf("run-%d", time.Now().Unix())
		runOutputDir := filepath.Join(os.TempDir(), "pred-ate-"+runID)
		if err := os.MkdirAll(runOutputDir, 0755); err != nil {
			return fmt.Errorf("failed to create run output directory: %w", err)
		}
		defer os.RemoveAll(runOutputDir)

		vmCfg := vm.Config{
			BaseImage:       globalCfg.BaseImage,
			WorkspaceDir:    workspaceAbs,
			FindingsDir:     findingsAbs,
			OutputDir:       runOutputDir,
			GeminiAPIKey:    globalCfg.GeminiAPIKey,
			Hint:            hintFlag,
			MaxTotalTokens:  maxTokensFlag,
			MaxInputTokens:  maxInputTokensFlag,
			MaxOutputTokens: maxOutputTokensFlag,
			MemoryMB:        globalCfg.MemoryMB,
			CPUs:            globalCfg.CPUs,
			Stdout:          cmd.OutOrStdout(),
			Stderr:          cmd.ErrOrStderr(),
		}

		cmd.Printf("==> [pred-ate] Starting session %s\n", runID)
		cmd.Printf("    Workspace: %s (mounted read-only)\n", workspaceAbs)
		cmd.Printf("    Findings:  %s (mounted read-only)\n", findingsAbs)
		if hintFlag != "" {
			cmd.Printf("    Hint:      %s\n", hintFlag)
		}
		cmd.Printf("    RAM:       %d MB | vCPUs: %d\n", globalCfg.MemoryMB, globalCfg.CPUs)
		cmd.Printf("    Live Logs: tail -f %s/agent.log\n\n", runOutputDir)

		if err := vm.Run(ctx, vmCfg); err != nil {
			if ctx.Err() != nil {
				cmd.Println("\n[!] Session aborted by user (Ctrl+C). QEMU terminated and temporary overlay deleted.")
				return nil
			}
			return fmt.Errorf("VM run encountered an error: %w", err)
		}

		cmd.Println("\n==> [pred-ate] VM execution finished. Inspecting results...")

		promoted, err := findings.PromoteFinding(runOutputDir, findingsAbs)
		if err != nil {
			return fmt.Errorf("failed to promote finding: %w", err)
		}

		if promoted == nil {
			cmd.Println("[-] No new finding recorded in this run.")
			return nil
		}

		cmd.Println("\n========================================================")
		cmd.Printf("[+] Finding Recorded: %s\n", promoted.ID)
		cmd.Printf("    Verdict:    %s\n", promoted.Verdict)
		cmd.Printf("    Subsystem:  %s\n", promoted.Subsystem)
		cmd.Printf("    Hypothesis: %s\n", promoted.TestedHypothesis)
		if promoted.Reproducer != "" {
			cmd.Printf("    Reproducer: %s\n", promoted.Reproducer)
		}
		cmd.Printf("    Report:     %s\n", promoted.FilePath)
		cmd.Println("========================================================")

		return nil
	},
}

func init() {
	runCmd.Flags().StringVarP(&hintFlag, "hint", "H", "", "Operator suspicion, focus area, or specific subsystem to guide the agent")
	runCmd.Flags().Int64Var(&maxTokensFlag, "max-tokens", 2_000_000, "Cap on total tokens (input+output+thinking) the agent may consume in one session; 0 disables the cap")
	runCmd.Flags().Int64Var(&maxInputTokensFlag, "max-input-tokens", 0, "Independent cap on input tokens for the session; 0 leaves it unset (only --max-tokens applies)")
	runCmd.Flags().Int64Var(&maxOutputTokensFlag, "max-output-tokens", 0, "Independent cap on output tokens (candidates+thinking) for the session; 0 leaves it unset (only --max-tokens applies)")
	rootCmd.AddCommand(runCmd)
}
