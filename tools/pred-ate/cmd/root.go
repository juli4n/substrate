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
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type GlobalConfig struct {
	BaseImage    string
	Workspace    string
	FindingsDir  string
	GeminiAPIKey string
	MemoryMB     int
	CPUs         int
}

var globalCfg GlobalConfig

var rootCmd = &cobra.Command{
	Use:   "pred-ate",
	Short: "Autonomous bug-hunting and reliability testing agent for Agent Substrate",
	Long: `pred-ate (Mascot: YOLO the octopus) is an autonomous bug-hunting runner
for Agent Substrate ("ate"). It boots disposable QEMU VM sandboxes with nested KVM,
shares source code read-only, checks prior findings for novelty, executes
stress/fuzzing hypotheses, and records structured bug/resilience reports.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	homeDir, _ := os.UserHomeDir()
	defaultBaseImage := os.Getenv("PREDATE_BASE_IMAGE")
	if defaultBaseImage == "" {
		defaultBaseImage = filepath.Join(homeDir, ".pred-ate", "base.qcow2")
	}

	cwd, _ := os.Getwd()

	rootCmd.PersistentFlags().StringVar(&globalCfg.BaseImage, "base-image", defaultBaseImage, "Path to base QCOW2 image [env: PREDATE_BASE_IMAGE]")
	rootCmd.PersistentFlags().StringVar(&globalCfg.Workspace, "workspace", cwd, "Path to Substrate source repository")
	rootCmd.PersistentFlags().StringVar(&globalCfg.FindingsDir, "findings-dir", filepath.Join(cwd, "artifacts", "findings"), "Directory storing persistent findings")
	rootCmd.PersistentFlags().StringVar(&globalCfg.GeminiAPIKey, "gemini-api-key", os.Getenv("GEMINI_API_KEY"), "Gemini API key for autonomous agent [env: GEMINI_API_KEY]")
	rootCmd.PersistentFlags().IntVar(&globalCfg.MemoryMB, "memory", 16384, "VM memory in megabytes")
	rootCmd.PersistentFlags().IntVar(&globalCfg.CPUs, "cpus", 8, "Number of vCPUs for VM")
}
