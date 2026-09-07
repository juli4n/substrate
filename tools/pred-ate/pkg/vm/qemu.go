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

package vm

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agent-substrate/substrate/tools/pred-ate/pkg/agent"
)

type Config struct {
	BaseImage       string
	WorkspaceDir    string
	FindingsDir     string
	OutputDir       string
	GeminiAPIKey    string
	Hint            string
	MaxTotalTokens  int64
	MaxInputTokens  int64
	MaxOutputTokens int64
	MemoryMB        int
	CPUs            int
	Stdout          io.Writer
	Stderr          io.Writer
}

func CheckHostPrerequisites() error {
	tools := []string{"qemu-img", "qemu-system-x86_64"}
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("required virtualization tool '%s' is not installed or not in PATH", t)
		}
	}

	_, hasXorriso := exec.LookPath("xorriso")
	_, hasGeniso := exec.LookPath("genisoimage")
	if hasXorriso != nil && hasGeniso != nil {
		return fmt.Errorf("neither 'xorriso' nor 'genisoimage' was found; needed to generate cloud-init seed ISO")
	}

	return nil
}

func hasKVM() bool {
	info, err := os.Stat("/dev/kvm")
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func CreateOverlay(baseImage, overlayPath string) error {
	if _, err := os.Stat(baseImage); err != nil {
		return fmt.Errorf("base image not found at %s: %w", baseImage, err)
	}

	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", baseImage, "-F", "qcow2", overlayPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img failed: %v, output: %s", err, out)
	}
	return nil
}

func GenerateCloudInitISO(scratchDir, isoPath string, cfg Config) error {
	metaDataPath := filepath.Join(scratchDir, "meta-data")
	userDataPath := filepath.Join(scratchDir, "user-data")

	metaData := "instance-id: pred-ate-run\nlocal-hostname: pred-ate-sandbox\n"
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return err
	}

	// Indent the embedded python script for cloud-init YAML write_files block
	indentedScript := indentLines(agent.HunterScript, 6)

	userData := fmt.Sprintf(`#cloud-config
write_files:
  - path: /root/hunter.py
    permissions: '0755'
    content: |
%s
  - path: /root/run.sh
    permissions: '0755'
    content: |
      #!/usr/bin/env bash
      set -eo pipefail

      # cloud-init's runcmd module runs this via a bare systemd service, not a
      # login shell, so $HOME/$USER/$LOGNAME are unset by default. Tools the
      # agent invokes via run_command (kind, docker, kubectl, git, ...) all
      # depend on $HOME for their config/state dirs, so set it before anything
      # else runs.
      export HOME=/root
      export USER=root
      export LOGNAME=root

      # Disable the login getty on ttyS0 before touching that device ourselves.
      # agetty calls vhangup() on (re)spawn (it has Restart=always), which
      # invalidates any other process's existing fd to the same tty and makes
      # our own console output start failing with "Input/output error" once
      # it respawns mid-run.
      systemctl stop serial-getty@ttyS0.service 2>/dev/null || true
      systemctl mask serial-getty@ttyS0.service 2>/dev/null || true

      # Mount the output share first, before touching console output, so the
      # rest of this script can persist a full session log to the host.
      mkdir -p /mnt/host_src /mnt/known_findings /mnt/output
      if ! mount -t 9p -o trans=virtio,version=9p2000.L,rw output_dir /mnt/output; then
        echo "warning: output_dir mount failed; agent.log will not be persisted to host" > /dev/ttyS0
      fi

      # Mirror ALL script output (bootstrap steps AND the agent) to both the
      # live serial console and /mnt/output/agent.log, so a failure anywhere
      # -- including before the agent ever starts -- is diagnosable from the
      # host after the VM powers off, not just visible transiently on tty.
      exec > >(tee -a /mnt/output/agent.log > /dev/ttyS0) 2>&1

      # Guarantee the VM always powers off, even if a step below fails, so a
      # failed run ends the session instead of idling forever with -e/pipefail.
      trap 'sync; poweroff -f' EXIT

      echo "==> [pred-ate] Sandbox bootstrap starting..."

      mount -t 9p -o trans=virtio,version=9p2000.L,ro host_src /mnt/host_src || echo "warning: host_src mount failed"
      mount -t 9p -o trans=virtio,version=9p2000.L,ro known_findings /mnt/known_findings || echo "warning: known_findings mount failed"

      # Block cloud metadata service guardrail
      iptables -A OUTPUT -d 169.254.169.254 -j DROP 2>/dev/null || echo "warning: iptables metadata guardrail failed to apply"

      # Stream clean source to internal workspace via tar (fast single-stream over 9p)
      echo "==> [pred-ate] Fast-streaming source repository into sandbox workspace..."
      mkdir -p /workspace/substrate
      if [ -d /mnt/host_src ]; then
        set +e
        tar -C /mnt/host_src -cf - . | tar -C /workspace/substrate -xf -
        tar_status=("${PIPESTATUS[@]}")
        set -e
        echo "==> [pred-ate] repo sync tar exit codes: create=${tar_status[0]} extract=${tar_status[1]}"
        # GNU tar exits 1 for benign warnings (e.g. "file changed as we read
        # it"), which are expected when copying a live working tree; only
        # exit codes >=2 indicate a real, fatal error.
        if [ "${tar_status[0]}" -ge 2 ] || [ "${tar_status[1]}" -ge 2 ]; then
          echo "==> [pred-ate] ERROR: repo sync into workspace failed"
          exit 1
        fi
      fi
      cd /workspace/substrate

      # The 9p mount preserves the host's numeric UID on the tar-streamed
      # files (not root's), so git -- running as root here -- refuses to
      # operate on it as a "dubious ownership" safety measure. This is an
      # ephemeral, single-tenant sandbox, so trust the whole tree.
      git config --global --add safe.directory '*'

      # Bootstrap the Kind cluster and deploy ate-system deterministically,
      # before the agent ever starts. This used to be the agent's own "Phase
      # 1", spending tokens and retry budget on infrastructure setup that has
      # no need for LLM judgment and is far more reliable as a plain script.
      echo "==> [pred-ate] Bootstrapping Kind cluster..."
      hack/create-kind-cluster.sh || {
        echo "==> [pred-ate] ERROR: create-kind-cluster.sh failed"
        exit 1
      }
      echo "==> [pred-ate] Deploying ate-system..."
      hack/install-ate-kind.sh --deploy-ate-system || {
        echo "==> [pred-ate] ERROR: install-ate-kind.sh --deploy-ate-system failed"
        exit 1
      }
      echo "==> [pred-ate] Waiting for one-shot init Jobs to complete..."
      # Jobs (e.g. rustfs-bucket-init) run to completion and terminate; they
      # have their own "complete" condition and must not be included in the
      # pod-readiness wait below, since a terminated pod never satisfies
      # condition=ready no matter when in its lifecycle the wait observes it.
      kubectl wait --for=condition=complete job --all -n ate-system --timeout=300s || {
        echo "==> [pred-ate] ERROR: init Jobs did not complete in time"
        exit 1
      }
      echo "==> [pred-ate] Waiting for ate-system pods to be ready..."
      # Kubernetes auto-labels every pod a Job creates with job-name, so this
      # excludes all Job-managed pods (already handled above) and only waits
      # on the actual long-running service pods.
      kubectl wait --for=condition=ready pod --all -n ate-system --timeout=300s \
        -l '!job-name' || {
        echo "==> [pred-ate] ERROR: ate-system pods did not become ready in time"
        exit 1
      }
      echo "==> [pred-ate] Cluster ready."

      # Self-heal: ensure google-antigravity is installed. This should be a
      # no-op on any image built by 'init-image --build', which already bakes
      # in both python3-pip and google-antigravity; it only exists to cover a
      # stale or manually-prepared base image (see README's manual setup
      # path). pip3 itself is guaranteed present by the bake's package list,
      # so unlike google-antigravity there is nothing to provision for it.
      if ! python3 -c "import google.antigravity" 2>/dev/null; then
        echo "==> [pred-ate] Installing google-antigravity Python SDK..."
        python3 -m pip install --break-system-packages --ignore-installed google-antigravity || {
          echo "==> [pred-ate] ERROR: failed to install google-antigravity"
          exit 1
        }
      fi

      export GEMINI_API_KEY="%s"
      export PREDATE_HINT="%s"
      export PREDATE_MAX_TOTAL_TOKENS="%d"
      export PREDATE_MAX_INPUT_TOKENS="%d"
      export PREDATE_MAX_OUTPUT_TOKENS="%d"

      echo "==> [pred-ate] Launching autonomous bug hunter..."
      python3 -u /root/hunter.py || true

      echo "==> [pred-ate] Autonomous run complete. Powering down..."

runcmd:
  - /root/run.sh
`, indentedScript, cfg.GeminiAPIKey, escapeQuotes(cfg.Hint), cfg.MaxTotalTokens, cfg.MaxInputTokens, cfg.MaxOutputTokens)

	if err := os.WriteFile(userDataPath, []byte(userData), 0644); err != nil {
		return err
	}

	// Build ISO using xorriso or genisoimage
	if _, err := exec.LookPath("xorriso"); err == nil {
		cmd := exec.Command("xorriso", "-as", "mkisofs", "-R", "-V", "cidata", "-o", isoPath, userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("xorriso failed: %v, output: %s", err, out)
		}
		return nil
	}

	cmd := exec.Command("genisoimage", "-output", isoPath, "-volid", "cidata", "-joliet", "-rock", userDataPath, metaDataPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("genisoimage failed: %v, output: %s", err, out)
	}
	return nil
}

func Run(ctx context.Context, cfg Config) error {
	if err := CheckHostPrerequisites(); err != nil {
		return err
	}

	scratchDir, err := os.MkdirTemp("", "pred-ate-run-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	overlayPath := filepath.Join(scratchDir, "overlay.qcow2")
	isoPath := filepath.Join(scratchDir, "seed.iso")

	if cfg.Stdout != nil {
		fmt.Fprintf(cfg.Stdout, "==> Creating ephemeral QCOW2 overlay from %s...\n", cfg.BaseImage)
	}
	if err := CreateOverlay(cfg.BaseImage, overlayPath); err != nil {
		return err
	}

	if cfg.Stdout != nil {
		fmt.Fprintln(cfg.Stdout, "==> Generating Cloud-Init seed ISO...")
	}
	if err := GenerateCloudInitISO(scratchDir, isoPath, cfg); err != nil {
		return err
	}

	args := []string{
		"-cpu", "host",
		"-m", fmt.Sprintf("%dM", cfg.MemoryMB),
		"-smp", fmt.Sprintf("%d", cfg.CPUs),
		"-display", "none",
		"-serial", "stdio",
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", overlayPath),
		"-cdrom", isoPath,
		"-virtfs", fmt.Sprintf("local,path=%s,mount_tag=host_src,security_model=none,readonly=on", cfg.WorkspaceDir),
		"-virtfs", fmt.Sprintf("local,path=%s,mount_tag=known_findings,security_model=none,readonly=on", cfg.FindingsDir),
		"-virtfs", fmt.Sprintf("local,path=%s,mount_tag=output_dir,security_model=none,readonly=off", cfg.OutputDir),
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0",
	}

	if hasKVM() {
		args = append([]string{"-enable-kvm"}, args...)
		if cfg.Stdout != nil {
			fmt.Fprintln(cfg.Stdout, "==> Hardware KVM acceleration enabled.")
		}
	} else if cfg.Stdout != nil {
		fmt.Fprintln(cfg.Stdout, "==> Warning: KVM not detected; falling back to emulation.")
	}

	if cfg.Stdout != nil {
		fmt.Fprintln(cfg.Stdout, "==> Booting isolated QEMU sandbox...")
	}

	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("QEMU execution exited with error: %w", err)
	}

	return nil
}

func BakeBaseImage(ctx context.Context, targetPath string, force bool, stdout, stderr io.Writer) error {
	if err := CheckHostPrerequisites(); err != nil {
		return err
	}

	if _, err := os.Stat(targetPath); err == nil && !force {
		return fmt.Errorf("target base image '%s' already exists; use --force to overwrite", targetPath)
	}

	destDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	scratchDir, err := os.MkdirTemp("", "pred-ate-bake-*")
	if err != nil {
		return fmt.Errorf("failed to create scratch dir: %w", err)
	}
	defer os.RemoveAll(scratchDir)

	cloudImgURL := "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
	tempImg := filepath.Join(scratchDir, "base.qcow2")

	if stdout != nil {
		fmt.Fprintf(stdout, "==> Downloading Ubuntu 24.04 cloud image from %s...\n", cloudImgURL)
	}

	curlCmd := exec.CommandContext(ctx, "curl", "-fL", "--progress-bar", "-o", tempImg, cloudImgURL)
	curlCmd.Stdout = stdout
	curlCmd.Stderr = stderr
	if err := curlCmd.Run(); err != nil {
		return fmt.Errorf("failed to download cloud image: %w", err)
	}

	if stdout != nil {
		fmt.Fprintln(stdout, "==> Resizing base image to 30GB...")
	}
	resizeCmd := exec.CommandContext(ctx, "qemu-img", "resize", tempImg, "30G")
	if out, err := resizeCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to resize image: %v, output: %s", err, out)
	}

	if stdout != nil {
		fmt.Fprintln(stdout, "==> Generating provisioning Cloud-Init ISO...")
	}

	userDataPath := filepath.Join(scratchDir, "user-data")
	metaDataPath := filepath.Join(scratchDir, "meta-data")
	isoPath := filepath.Join(scratchDir, "seed.iso")

	metaData := "instance-id: pred-ate-bake\nlocal-hostname: pred-ate-baker\n"
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return err
	}

	userData := `#cloud-config
package_update: true
packages:
  - curl
  - git
  - make
  - python3
  - python3-pip
  - python3-venv
  - iptables
runcmd:
  - systemctl stop serial-getty@ttyS0.service 2>/dev/null || true
  - systemctl mask serial-getty@ttyS0.service 2>/dev/null || true
  - echo "==> [Bake] Configuring kernel modules for 9p..."
  - modprobe 9p || true
  - modprobe 9pnet_virtio || true
  - echo "9p" >> /etc/modules
  - echo "9pnet_virtio" >> /etc/modules

  - echo "==> [Bake] Installing Docker..."
  - curl -fsSL https://get.docker.com -o /tmp/get-docker.sh && sh /tmp/get-docker.sh
  - systemctl enable --now docker

  - echo "==> [Bake] Installing Go 1.27..."
  - curl -fsSL https://go.dev/dl/go1.27.0.linux-amd64.tar.gz -o /tmp/go.tar.gz || curl -fsSL https://go.dev/dl/go1.24.0.linux-amd64.tar.gz -o /tmp/go.tar.gz
  - rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
  - ln -sf /usr/local/go/bin/go /usr/local/bin/go

  - echo "==> [Bake] Installing Kind and Kubectl..."
  - curl -Lo /usr/local/bin/kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64 && chmod +x /usr/local/bin/kind
  - curl -Lo /usr/local/bin/kubectl "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && chmod +x /usr/local/bin/kubectl

  - echo "==> [Bake] Installing google-antigravity Python SDK..."
  - python3 -m pip install --break-system-packages --ignore-installed google-antigravity
  - 'python3 -c "import google.antigravity" && echo "==> [Bake] google-antigravity verified importable." || echo "==> [Bake] FATAL: google-antigravity did not install correctly! Runtime self-heal will have to install it on every run instead."'

  - echo "==> [Bake Complete] Base image provisioned successfully! Powering down..."
  - sync
  - poweroff -f
`
	if err := os.WriteFile(userDataPath, []byte(userData), 0644); err != nil {
		return err
	}

	if _, err := exec.LookPath("xorriso"); err == nil {
		cmd := exec.CommandContext(ctx, "xorriso", "-as", "mkisofs", "-R", "-V", "cidata", "-o", isoPath, userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("xorriso failed: %v, output: %s", err, out)
		}
	} else {
		cmd := exec.CommandContext(ctx, "genisoimage", "-output", isoPath, "-volid", "cidata", "-joliet", "-rock", userDataPath, metaDataPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("genisoimage failed: %v, output: %s", err, out)
		}
	}

	args := []string{
		"-cpu", "host",
		"-m", "4096",
		"-smp", "4",
		"-display", "none",
		"-serial", "stdio",
		"-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", tempImg),
		"-cdrom", isoPath,
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0",
	}
	if hasKVM() {
		args = append([]string{"-enable-kvm"}, args...)
		if stdout != nil {
			fmt.Fprintln(stdout, "==> Hardware KVM acceleration enabled for baking.")
		}
	}

	if stdout != nil {
		fmt.Fprintln(stdout, "==> Booting VM to provision base packages (Docker, Go, Kind, Kubectl, Antigravity)...")
		fmt.Fprintln(stdout, "    The VM will automatically shut down when provisioning completes.")
	}

	qemuCmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	qemuCmd.Stdout = stdout
	qemuCmd.Stderr = stderr

	if err := qemuCmd.Run(); err != nil {
		return fmt.Errorf("QEMU provisioning run exited with error: %w", err)
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "==> Moving baked image to %s...\n", targetPath)
	}
	if err := os.Rename(tempImg, targetPath); err != nil {
		if err := copyFile(tempImg, targetPath); err != nil {
			return fmt.Errorf("failed to save final base image: %w", err)
		}
	}

	if stdout != nil {
		fmt.Fprintf(stdout, "[✓] Base image successfully baked and ready at %s!\n", targetPath)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func indentLines(text string, spaces int) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
