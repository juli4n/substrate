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

	"github.com/agent-substrate/substrate/tools/pred-ate/pkg/vm"
	"github.com/spf13/cobra"
)

var (
	buildImage bool
	forceBuild bool
)

var initImageCmd = &cobra.Command{
	Use:   "init-image",
	Short: "Check host prerequisites and build or configure the base QCOW2 image",
	Long: `Verifies that host QEMU, KVM, and ISO utilities are ready.
When run with --build, downloads the official Ubuntu 24.04 cloud image and
provisions it with Docker, Go 1.27, Kind, Kubectl, and Antigravity.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		cmd.Println("==> Checking host virtualization prerequisites...")
		if err := vm.CheckHostPrerequisites(); err != nil {
			return err
		}
		cmd.Println("[✓] QEMU utilities and ISO builder found in PATH.")

		if _, err := os.Stat("/dev/kvm"); err == nil {
			cmd.Println("[✓] /dev/kvm detected and accessible.")
		} else {
			cmd.Println("[!] Warning: /dev/kvm not found. Nested virtualization will run under emulation.")
		}

		destDir := filepath.Dir(globalCfg.BaseImage)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return err
		}

		cmd.Println("\n==> Base Image Configuration:")
		cmd.Printf("    Target Path: %s\n\n", globalCfg.BaseImage)

		if buildImage {
			return vm.BakeBaseImage(ctx, globalCfg.BaseImage, forceBuild, cmd.OutOrStdout(), cmd.ErrOrStderr())
		}

		if _, err := os.Stat(globalCfg.BaseImage); err == nil {
			cmd.Println("[✓] Base image already exists at target path!")
			cmd.Println("    To rebuild it, run: go run ./tools/pred-ate init-image --build --force")
			return nil
		}

		cmd.Println("The base image does not exist yet. You can build it automatically:")
		cmd.Println("  go run ./tools/pred-ate init-image --build")
		cmd.Println()
		cmd.Println("Or prepare it manually by following these steps:")
		cmd.Println("  1. Linux (Ubuntu 24.04 or Debian 12 cloud image)")
		cmd.Println("  2. Docker (dockerd running)")
		cmd.Println("  3. Go 1.27+")
		cmd.Println("  4. Kind and Kubectl")
		cmd.Println("  5. Python 3 + `pip install google-antigravity`")
		cmd.Println("  6. 9p filesystem support (`modprobe 9pnet_virtio 9p`)")
		cmd.Println("\nTo prepare manually:")
		cmd.Println("  curl -Lo /tmp/ubuntu.img https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img")
		cmd.Println("  qemu-img resize /tmp/ubuntu.img 30G")
		fmt.Printf("  mv /tmp/ubuntu.img %s\n\n", globalCfg.BaseImage)

		return nil
	},
}

func init() {
	initImageCmd.Flags().BoolVar(&buildImage, "build", false, "Automatically download and provision the base QCOW2 image")
	initImageCmd.Flags().BoolVar(&forceBuild, "force", false, "Overwrite existing base image if it already exists")
	rootCmd.AddCommand(initImageCmd)
}
