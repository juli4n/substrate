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

// Package harness runs ate-api against fakes of everything it talks to, for
// functional tests that drive it only through its public API.
//
// Each test gets its own ate-api, database, k8s namespace, nodes, atelets,
// object store and volume plugin, so tests run in parallel. A test package
// using the harness calls Main from its TestMain.
package harness

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store/storetest"
	"github.com/agent-substrate/substrate/internal/installdefaults"
	"github.com/agent-substrate/substrate/internal/testenv"
	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/client/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	// SandboxConfigName is the cluster's gVisor SandboxConfig that harness
	// templates name.
	SandboxConfigName = "harness-gvisor"
	// PauseImage is the pause image SandboxConfigName carries.
	PauseImage = "pause@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// restConfig reaches the envtest cluster Main starts. It is shared by every
// test in the package; what each test creates in it is scoped to the test.
var restConfig *rest.Config

// Main starts the envtest cluster shared by the package's tests, runs them,
// and tears the cluster and the PostgreSQL container down.
func Main(m *testing.M) {
	cfg, stopEnv := testenv.Start()
	restConfig = cfg
	if err := setupCluster(cfg); err != nil {
		stopEnv()
		log.Fatalf("setting up the harness cluster: %v", err)
	}
	code := m.Run()
	stopEnv()
	storetest.Shutdown()
	os.Exit(code)
}

// setupCluster creates the cluster-scoped objects every test reads and none
// changes.
func setupCluster(cfg *rest.Config) error {
	ctx := context.Background()
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	ac, err := versioned.NewForConfig(cfg)
	if err != nil {
		return err
	}

	if _, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: installdefaults.SystemNamespace},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %s: %w", installdefaults.SystemNamespace, err)
	}

	if _, err := kc.StorageV1().StorageClasses().Create(ctx, &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: StorageClass},
		Provisioner: VolumeDriver,
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating StorageClass %s: %w", StorageClass, err)
	}

	if _, err := ac.ApiV1alpha1().SandboxConfigs().Create(ctx, &atev1alpha1.SandboxConfig{
		ObjectMeta: metav1.ObjectMeta{Name: SandboxConfigName},
		Spec: atev1alpha1.SandboxConfigSpec{
			SandboxClass: atev1alpha1.SandboxClassGvisor,
			PauseImage:   PauseImage,
			Assets: map[string]map[string]atev1alpha1.AssetFile{
				"amd64": {"runsc": {
					URL:    "gs://gvisor/releases/nightly/2026-05-19/x86_64/runsc",
					SHA256: "a397be1abc2420d26bce6c70e6e2ff96c73aaaab929756c56f5e2089ea842b63",
				}},
				"arm64": {"runsc": {
					URL:    "gs://gvisor/releases/nightly/2026-05-19/aarch64/runsc",
					SHA256: "1ba2366ae2efceba166046f51a4104f9261c9cb72c6db8f5b3fe2dc57dea86b9",
				}},
			},
		},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating SandboxConfig %s: %w", SandboxConfigName, err)
	}
	return nil
}
