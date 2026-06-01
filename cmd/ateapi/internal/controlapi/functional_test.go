//  Copyright 2026 Google LLC
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package controlapi

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store/ateredis"
	"github.com/agent-substrate/substrate/internal/ateinterceptors"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/client/clientset/versioned"
	"github.com/agent-substrate/substrate/pkg/client/informers/externalversions"
	listersv1alpha1 "github.com/agent-substrate/substrate/pkg/client/listers/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	testEnv    *envtest.Environment
	cfg        *rest.Config
	fakeAtelet = &FakeAteletServer{}
)

func TestMain(m *testing.M) {
	cmd := exec.Command("bash", "../../../../hack/run-tool.sh", "setup-envtest", "use", "--print", "path")
	out, err := cmd.Output()
	if err != nil {
		os.Exit(1)
	}
	binaryAssetsDirectory := strings.TrimSpace(string(out))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../../../manifests/ate-install/generated"},
		BinaryAssetsDirectory: binaryAssetsDirectory,
	}

	cfg, err = testEnv.Start()
	if err != nil {
		os.Exit(1)
	}

	// Create ate-system namespace
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		os.Exit(1)
	}
	_, err = k8sClient.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "ate-system"},
	}, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		os.Exit(1)
	}

	// Create shared Atelet Pod
	ateletPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "atelet-shared",
			Namespace: "ate-system",
			Labels: map[string]string{
				"app": "atelet",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "node1",
			Containers: []corev1.Container{
				{Name: "main", Image: "nginx"},
			},
		},
	}
	createdAtelet, err := k8sClient.CoreV1().Pods("ate-system").Create(context.Background(), ateletPod, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		os.Exit(1)
	}
	if err == nil {
		createdAtelet.Status.PodIPs = []corev1.PodIP{{IP: "127.0.0.1"}}
		createdAtelet.Status.Phase = corev1.PodRunning
		_, err = k8sClient.CoreV1().Pods("ate-system").UpdateStatus(context.Background(), createdAtelet, metav1.UpdateOptions{})
		if err != nil {
			os.Exit(1)
		}
	}

	// Start Fake Atelet Server on port 8085
	ateletGrpcServer := grpc.NewServer()
	ateletpb.RegisterAteomHerderServer(ateletGrpcServer, fakeAtelet)
	ateletLis, err := net.Listen("tcp", "127.0.0.1:8085")
	if err != nil {
		os.Exit(1)
	}
	go func() {
		if err := ateletGrpcServer.Serve(ateletLis); err != nil {
			fmt.Printf("atelet grpc server exited: %v\n", err)
		}
	}()

	code := m.Run()

	ateletGrpcServer.Stop()

	err = testEnv.Stop()
	if err != nil {
		os.Exit(1)
	}

	os.Exit(code)
}

// FakeAteletServer implements ateletpb.AteomHerderServer for tests.
type FakeAteletServer struct {
	ateletpb.UnimplementedAteomHerderServer

	Lock sync.Mutex

	RunCalled bool

	CheckpointCalled bool

	RestoreCalled bool
	FailRestore   error
	RestoreDelay  time.Duration

	// Pod info returned by Run and Restore.
	ReturnPodNamespace string
	ReturnPodName      string
	ReturnPodIP        string
}

func (f *FakeAteletServer) Reset() {
	f.Lock.Lock()
	defer f.Lock.Unlock()

	f.RunCalled = false
	f.CheckpointCalled = false
	f.RestoreCalled = false
	f.FailRestore = nil
	f.RestoreDelay = 0
	f.ReturnPodNamespace = ""
	f.ReturnPodName = ""
	f.ReturnPodIP = ""
}

func (f *FakeAteletServer) WatchCapacity(_ *ateletpb.WatchCapacityRequest, stream ateletpb.AteomHerder_WatchCapacityServer) error {
	// Send an empty snapshot and then block until the stream is closed.
	// Tests inject capacity directly via CapacityManager.ForceCapacity.
	stream.Send(&ateletpb.CapacitySnapshot{}) //nolint:errcheck
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (f *FakeAteletServer) Run(_ context.Context, _ *ateletpb.RunRequest) (*ateletpb.RunResponse, error) {
	f.Lock.Lock()
	defer f.Lock.Unlock()

	f.RunCalled = true

	return &ateletpb.RunResponse{
		WorkerPodNamespace: f.ReturnPodNamespace,
		WorkerPodName:      f.ReturnPodName,
		WorkerPodIp:        f.ReturnPodIP,
	}, nil
}

func (f *FakeAteletServer) Checkpoint(_ context.Context, _ *ateletpb.CheckpointRequest) (*ateletpb.CheckpointResponse, error) {
	f.Lock.Lock()
	defer f.Lock.Unlock()

	f.CheckpointCalled = true

	return &ateletpb.CheckpointResponse{}, nil
}

func (f *FakeAteletServer) Restore(_ context.Context, _ *ateletpb.RestoreRequest) (*ateletpb.RestoreResponse, error) {
	f.Lock.Lock()
	defer f.Lock.Unlock()

	f.RestoreCalled = true
	if f.RestoreDelay > 0 {
		time.Sleep(f.RestoreDelay)
	}
	if f.FailRestore != nil {
		return nil, f.FailRestore
	}
	return &ateletpb.RestoreResponse{
		WorkerPodNamespace: f.ReturnPodNamespace,
		WorkerPodName:      f.ReturnPodName,
		WorkerPodIp:        f.ReturnPodIP,
	}, nil
}

type testContext struct {
	mr                  *miniredis.Miniredis
	service             *Service
	client              ateapipb.ControlClient
	k8sClient           kubernetes.Interface
	substrateClient     versioned.Interface
	persistence         *ateredis.Persistence
	ateletManager       *AteletManager
	fakeAtelet          *FakeAteletServer
	cleanup             func()
	actorTemplateLister listersv1alpha1.ActorTemplateLister
}

// setupTest sets up a fully isolated test environment.
func setupTest(t *testing.T, ns string) *testContext {
	t.Helper()
	// 1. Start Miniredis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	rdb := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{mr.Addr()},
	})
	persistence := ateredis.NewPersistence(rdb)

	// 2. Initialize Clientsets using global cfg
	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		mr.Close()
		t.Fatalf("failed to create k8s clientset: %v", err)
	}

	substrateClient, err := versioned.NewForConfig(cfg)
	if err != nil {
		mr.Close()
		t.Fatalf("failed to create substrate clientset: %v", err)
	}

	// 3. Initialize Informers (atelet only; workers are tracked by atelet, not apiserver)
	ateletFactory, ateletInformer := AteletInformer(k8sClient)
	substrateInformerFactory := externalversions.NewSharedInformerFactory(substrateClient, 0)
	actorTemplateLister := substrateInformerFactory.Api().V1alpha1().ActorTemplates().Lister()

	ctx, cancel := context.WithCancel(context.Background())

	ateletFactory.Start(ctx.Done())
	substrateInformerFactory.Start(ctx.Done())

	ateletFactory.WaitForCacheSync(ctx.Done())
	substrateInformerFactory.WaitForCacheSync(ctx.Done())

	// 4. Initialize Service with AteletManager
	ateletManager := NewAteletManager(ctx, ateletInformer)
	service := NewService(persistence, actorTemplateLister, ateletManager)

	// 5. Start REAL gRPC Server for ATE API
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(ateinterceptors.ServerUnaryInterceptor))
	ateapipb.RegisterControlServer(grpcServer, service)

	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		cancel()
		mr.Close()
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Logf("grpc server exited: %v", err)
		}
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		grpcServer.Stop()
		cancel()
		mr.Close()
		t.Fatalf("failed to connect: %v", err)
	}

	client := ateapipb.NewControlClient(conn)

	// Call Reset on global mock
	fakeAtelet.Reset()

	// Create namespace
	_, err = k8sClient.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil {
		conn.Close()
		grpcServer.Stop()
		cancel()
		mr.Close()
		t.Fatalf("failed to create namespace %s: %v", ns, err)
	}

	cleanup := func() {
		conn.Close()
		grpcServer.Stop()
		cancel()
		mr.Close()
	}

	return &testContext{
		mr:                  mr,
		service:             service,
		client:              client,
		k8sClient:           k8sClient,
		substrateClient:     substrateClient,
		persistence:         persistence,
		ateletManager:       ateletManager,
		fakeAtelet:          fakeAtelet,
		cleanup:             cleanup,
		actorTemplateLister: actorTemplateLister,
	}
}

func namespaceForTest(baseName string) string {
	return fmt.Sprintf("%s-%d", baseName, time.Now().UnixNano())
}

func createTemplate(t *testing.T, tc *testContext, ns string) {
	t.Helper()
	actorTemplate := &atev1alpha1.ActorTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tmpl1",
			Namespace: ns,
		},
		Spec: atev1alpha1.ActorTemplateSpec{
			Runsc: atev1alpha1.RunscConfig{
				AMD64: &atev1alpha1.RunscPlatformConfig{
					URL:        "gs://gvisor/releases/nightly/2026-05-19/x86_64/runsc",
					SHA256Hash: "a397be1abc2420d26bce6c70e6e2ff96c73aaaab929756c56f5e2089ea842b63",
				},
			},
			PauseImage: "pause",
			Containers: []atev1alpha1.Container{
				{
					Name:    "main",
					Image:   "main",
					Command: []string{"/main"},
				},
			},
			WorkerPoolRef: corev1.ObjectReference{
				Namespace: ns,
				Name:      "pool1",
			},
		},
	}
	createdTemplate, err := tc.substrateClient.ApiV1alpha1().ActorTemplates(ns).Create(context.Background(), actorTemplate, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create actor template: %v", err)
	}

	createdTemplate.Status = atev1alpha1.ActorTemplateStatus{
		GoldenSnapshot: "gs://my-bucket/my-folder",
	}

	_, err = tc.substrateClient.ApiV1alpha1().ActorTemplates(ns).UpdateStatus(context.Background(), createdTemplate, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Wait for Informer cache to sync
	err = wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		tmpl, err := tc.actorTemplateLister.ActorTemplates(ns).Get("tmpl1")
		if err != nil {
			return false, nil // Retry if not found in cache yet
		}
		return tmpl.Status.GoldenSnapshot != "", nil
	})
	if err != nil {
		t.Fatalf("failed to wait for template status update in informer: %v", err)
	}
}

// setNodeCapacity injects free worker capacity for a node/pool directly into the CapacityManager.
// This replaces the old createWorkerPod helper, since worker pods are now tracked by atelet, not apiserver.
func setNodeCapacity(tc *testContext, nodeName, poolNs, poolName string, count int) {
	tc.ateletManager.ForceCapacity(nodeName, poolNs, poolName, count)
}

// TestCreateActor_Success tests the happy path for creating an actor.
func TestCreateActor_Success(t *testing.T) {
	ns := namespaceForTest("ns-create-success")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	createResp, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	want := &ateapipb.CreateActorResponse{
		Actor: &ateapipb.Actor{
			ActorId:                "id1",
			Version:                1,
			ActorTemplateNamespace: ns,
			ActorTemplateName:      "tmpl1",
			Status:                 ateapipb.Actor_STATUS_SUSPENDED,
		},
	}

	if diff := cmp.Diff(want, createResp, protocmp.Transform()); diff != "" {
		t.Errorf("CreateActor response mismatch (-want +got):\n%s", diff)
	}
}

// TestCreateActor_TemplateNotFound tests that creating an actor with a non-existent template fails with FailedPrecondition.
func TestCreateActor_TemplateNotFound(t *testing.T) {
	ns := namespaceForTest("ns-create-notfound")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "non-existent",
		ActorId:                "id1",
	})
	assertGrpcError(t, err, codes.FailedPrecondition, fmt.Sprintf("ActorTemplate %s/non-existent not found", ns))
}

// TestCreateActor_Duplicate tests that creating an actor with an existing ID fails.
func TestCreateActor_Duplicate(t *testing.T) {
	ns := namespaceForTest("ns-create-dup")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("first CreateActor failed: %v", err)
	}

	_, err = tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	assertGrpcError(t, err, codes.AlreadyExists, "Actor id1 already exists")
}

// TestGetActor_Found tests that an existing actor can be retrieved.
func TestGetActor_Found(t *testing.T) {
	ns := namespaceForTest("ns-get-found")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	createResp, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	id := createResp.GetActor().GetActorId()

	getResp, err := tc.client.GetActor(context.Background(), &ateapipb.GetActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}

	want := &ateapipb.GetActorResponse{
		Actor: createResp.GetActor(),
	}

	if diff := cmp.Diff(want, getResp, protocmp.Transform()); diff != "" {
		t.Errorf("GetActor response mismatch (-want +got):\n%s", diff)
	}
}

// TestGetActor_NotFound tests that retrieving a non-existent actor fails.
func TestGetActor_NotFound(t *testing.T) {
	ns := namespaceForTest("ns-get-notfound")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	_, err := tc.client.GetActor(context.Background(), &ateapipb.GetActorRequest{
		ActorId: "non-existent",
	})
	assertGrpcError(t, err, codes.NotFound, "Actor non-existent not found")
}

// TestListActors tests that all created actors can be listed.
func TestListActors(t *testing.T) {
	ns := namespaceForTest("ns-list-actors")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	resp1, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor 1 failed: %v", err)
	}
	resp2, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id2",
	})
	if err != nil {
		t.Fatalf("CreateActor 2 failed: %v", err)
	}

	listResp, err := tc.client.ListActors(context.Background(), &ateapipb.ListActorsRequest{})
	if err != nil {
		t.Fatalf("ListActors failed: %v", err)
	}

	if len(listResp.Actors) != 2 {
		t.Fatalf("expected 2 actors, got %d", len(listResp.Actors))
	}

	sort.Slice(listResp.Actors, func(i, j int) bool {
		return listResp.Actors[i].ActorId < listResp.Actors[j].ActorId
	})

	want := []*ateapipb.Actor{
		resp1.GetActor(),
		resp2.GetActor(),
	}
	sort.Slice(want, func(i, j int) bool {
		return want[i].ActorId < want[j].ActorId
	})

	if diff := cmp.Diff(want, listResp.Actors, protocmp.Transform()); diff != "" {
		t.Errorf("ListActors response mismatch (-want +got):\n%s", diff)
	}
}

// TestResumeActor tests the happy path for resuming a suspended actor.
func TestResumeActor(t *testing.T) {
	ns := namespaceForTest("ns-resume")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	tc.fakeAtelet.ReturnPodNamespace = ns
	tc.fakeAtelet.ReturnPodName = "worker-1"
	tc.fakeAtelet.ReturnPodIP = "127.0.0.1"

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}
	id := "id1"

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("ResumeActor failed: %v", err)
	}

	if !tc.fakeAtelet.RestoreCalled {
		t.Errorf("expected Restore to be called")
	}

	getResp, err := tc.client.GetActor(context.Background(), &ateapipb.GetActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}
	want := &ateapipb.GetActorResponse{
		Actor: &ateapipb.Actor{
			ActorId:                id,
			ActorTemplateNamespace: ns,
			ActorTemplateName:      "tmpl1",
			Status:                 ateapipb.Actor_STATUS_RUNNING,
			LastNodeName:           "node1",
			AteomPodNamespace:      ns,
			AteomPodName:           "worker-1",
			AteomPodIp:             "127.0.0.1",
		},
	}
	if diff := cmp.Diff(want, getResp, protocmp.Transform(), protocmp.IgnoreFields(&ateapipb.Actor{}, "version")); diff != "" {
		t.Errorf("GetActor response mismatch (-want +got):\n%s", diff)
	}
}

// TestResumeActor_NoWorkers tests that resuming fails when no capacity is available.
func TestResumeActor_NoWorkers(t *testing.T) {
	ns := namespaceForTest("ns-resume-no-workers")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	createResp, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	id := createResp.GetActor().GetActorId()

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	assertGrpcError(t, err, codes.ResourceExhausted, fmt.Sprintf("no atelets with free capacity for pool %s/pool1", ns))
}

// TestResumeActor_Reentrancy tests that if Restore fails (actor stuck in RESUMING),
// a subsequent ResumeActor call retries and succeeds.
func TestResumeActor_Reentrancy(t *testing.T) {
	ns := namespaceForTest("ns-resume-reentrancy")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}
	id := "id1"

	// STEP 1: Make Atelet FAIL on Restore.
	tc.fakeAtelet.FailRestore = fmt.Errorf("mock atelet failure")

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err == nil {
		t.Fatalf("expected ResumeActor to fail due to atelet error")
	}

	// Verify actor state is RESUMING in Redis.
	actor, err := tc.persistence.GetActor(context.Background(), id)
	if err != nil {
		t.Fatalf("failed to get actor from store: %v", err)
	}
	if actor.GetStatus() != ateapipb.Actor_STATUS_RESUMING {
		t.Errorf("expected status RESUMING, got %v", actor.GetStatus())
	}
	if actor.GetLastNodeName() != "node1" {
		t.Errorf("expected LastNodeName node1, got %v", actor.GetLastNodeName())
	}

	// STEP 2: Make Atelet SUCCEED.
	tc.fakeAtelet.FailRestore = nil
	tc.fakeAtelet.RestoreCalled = false

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("ResumeActor failed on retry: %v", err)
	}

	if !tc.fakeAtelet.RestoreCalled {
		t.Errorf("expected Restore to be called on retry")
	}

	// Verify actor state is RUNNING.
	actor, err = tc.persistence.GetActor(context.Background(), id)
	if err != nil {
		t.Fatalf("failed to get actor from store: %v", err)
	}
	if actor.GetStatus() != ateapipb.Actor_STATUS_RUNNING {
		t.Errorf("expected status RUNNING, got %v", actor.GetStatus())
	}
}

// TestSuspendActor tests the full workflow of suspending a running actor.
func TestSuspendActor(t *testing.T) {
	ns := namespaceForTest("ns-suspend")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	tc.fakeAtelet.ReturnPodNamespace = ns
	tc.fakeAtelet.ReturnPodName = "worker-1"
	tc.fakeAtelet.ReturnPodIP = "127.0.0.1"

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}
	id := "id1"

	// Resume first to make it running.
	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("ResumeActor failed: %v", err)
	}

	// Suspend.
	_, err = tc.client.SuspendActor(context.Background(), &ateapipb.SuspendActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("SuspendActor failed: %v", err)
	}

	if !tc.fakeAtelet.CheckpointCalled {
		t.Errorf("expected atelet Checkpoint to be called")
	}

	getResp, err := tc.client.GetActor(context.Background(), &ateapipb.GetActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}
	want := &ateapipb.GetActorResponse{
		Actor: &ateapipb.Actor{
			ActorId:                id,
			ActorTemplateNamespace: ns,
			ActorTemplateName:      "tmpl1",
			Status:                 ateapipb.Actor_STATUS_SUSPENDED,
			LastNodeName:           "node1",
		},
	}

	if diff := cmp.Diff(want, getResp, protocmp.Transform(), protocmp.IgnoreFields(&ateapipb.Actor{}, "version", "last_snapshot")); diff != "" {
		t.Errorf("GetActor response mismatch (-want +got):\n%s", diff)
	}
}

// TestValidation tests the negative validation cases for all gRPC methods.
func TestValidation(t *testing.T) {
	ns := namespaceForTest("ns-validation")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	t.Run("CreateActor", func(t *testing.T) {
		tests := []struct {
			name    string
			req     *ateapipb.CreateActorRequest
			wantMsg string
		}{
			{"missing namespace", &ateapipb.CreateActorRequest{ActorTemplateName: "tmpl1", ActorId: "id1"}, "actor_template_namespace is required"},
			{"missing template name", &ateapipb.CreateActorRequest{ActorTemplateNamespace: "ns1", ActorId: "id1"}, "actor_template_name is required"},
			{"missing actor id", &ateapipb.CreateActorRequest{ActorTemplateNamespace: "ns1", ActorTemplateName: "tmpl1"}, "actor_id is required"},
			{"invalid actor id (capitals)", &ateapipb.CreateActorRequest{ActorTemplateNamespace: "ns1", ActorTemplateName: "tmpl1", ActorId: "ID1"}, "invalid actor_id: must start and end with a lower case alphanumeric character, and consist only of lower case alphanumeric characters or '-'"},
			{"invalid actor id (special chars)", &ateapipb.CreateActorRequest{ActorTemplateNamespace: "ns1", ActorTemplateName: "tmpl1", ActorId: "id_1"}, "invalid actor_id: must start and end with a lower case alphanumeric character, and consist only of lower case alphanumeric characters or '-'"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := tc.client.CreateActor(context.Background(), tt.req)
				assertGrpcError(t, err, codes.InvalidArgument, tt.wantMsg)
			})
		}
	})

	t.Run("GetActor", func(t *testing.T) {
		tests := []struct {
			name    string
			req     *ateapipb.GetActorRequest
			wantMsg string
		}{
			{"missing id", &ateapipb.GetActorRequest{}, "id is required"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := tc.client.GetActor(context.Background(), tt.req)
				assertGrpcError(t, err, codes.InvalidArgument, tt.wantMsg)
			})
		}
	})

	t.Run("ResumeActor", func(t *testing.T) {
		tests := []struct {
			name    string
			req     *ateapipb.ResumeActorRequest
			wantMsg string
		}{
			{"missing id", &ateapipb.ResumeActorRequest{}, "id is required"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := tc.client.ResumeActor(context.Background(), tt.req)
				assertGrpcError(t, err, codes.InvalidArgument, tt.wantMsg)
			})
		}
	})

	t.Run("SuspendActor", func(t *testing.T) {
		tests := []struct {
			name    string
			req     *ateapipb.SuspendActorRequest
			wantMsg string
		}{
			{"missing id", &ateapipb.SuspendActorRequest{}, "id is required"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := tc.client.SuspendActor(context.Background(), tt.req)
				assertGrpcError(t, err, codes.InvalidArgument, tt.wantMsg)
			})
		}
	})

	t.Run("DeleteActor", func(t *testing.T) {
		tests := []struct {
			name    string
			req     *ateapipb.DeleteActorRequest
			wantMsg string
		}{
			{"missing id", &ateapipb.DeleteActorRequest{}, "actor_id is required"},
			{"invalid actor id (capitals)", &ateapipb.DeleteActorRequest{ActorId: "ID1"}, "invalid actor_id: must start and end with a lower case alphanumeric character, and consist only of lower case alphanumeric characters or '-'"},
			{"invalid actor id (special chars)", &ateapipb.DeleteActorRequest{ActorId: "id_1"}, "invalid actor_id: must start and end with a lower case alphanumeric character, and consist only of lower case alphanumeric characters or '-'"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := tc.client.DeleteActor(context.Background(), tt.req)
				assertGrpcError(t, err, codes.InvalidArgument, tt.wantMsg)
			})
		}
	})
}

func TestResumeActor_LockConflict(t *testing.T) {
	ns := namespaceForTest("ns-resume-conflict")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}
	id := "id1"

	// Set a delay on the fake Atelet to hold the lock.
	tc.fakeAtelet.RestoreDelay = 1 * time.Second

	// Launch Request A in a goroutine.
	errChan := make(chan error, 1)
	go func() {
		_, err := tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
			ActorId: id,
		})
		errChan <- err
	}()

	// Sleep a bit to ensure Request A acquired the lock.
	time.Sleep(200 * time.Millisecond)

	// Launch Request B (should fail due to lock conflict).
	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	assertGrpcError(t, err, codes.Aborted, "another operation is in progress for this actor")

	// Wait for Request A to finish.
	if errA := <-errChan; errA != nil {
		t.Fatalf("Request A failed: %v", errA)
	}
}

// TestResumeActor_DeclareIntent tests that when ResumeActor crashes mid-flight
// (actor stuck in RESUMING), a retry successfully recovers.
func TestResumeActor_DeclareIntent(t *testing.T) {
	ns := namespaceForTest("ns-resume-intent")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}
	id := "id1"

	// Simulate crash: fail the Restore call so actor is left in RESUMING.
	tc.fakeAtelet.FailRestore = fmt.Errorf("simulated crash")

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err == nil {
		t.Fatalf("expected ResumeActor to fail")
	}

	// Verify actor has RESUMING + declared intent node.
	actor, err := tc.persistence.GetActor(context.Background(), id)
	if err != nil {
		t.Fatalf("failed to get actor: %v", err)
	}
	if actor.GetStatus() != ateapipb.Actor_STATUS_RESUMING {
		t.Fatalf("expected RESUMING, got %v", actor.GetStatus())
	}
	if actor.GetLastNodeName() != "node1" {
		t.Fatalf("expected LastNodeName=node1, got %v", actor.GetLastNodeName())
	}

	// Fix the atelet and retry — should succeed idempotently.
	tc.fakeAtelet.FailRestore = nil
	tc.fakeAtelet.RestoreCalled = false

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: id,
	})
	if err != nil {
		t.Fatalf("retry ResumeActor failed: %v", err)
	}
	if !tc.fakeAtelet.RestoreCalled {
		t.Errorf("expected Restore to be called on retry")
	}

	actor, err = tc.persistence.GetActor(context.Background(), id)
	if err != nil {
		t.Fatalf("failed to get actor after retry: %v", err)
	}
	if actor.GetStatus() != ateapipb.Actor_STATUS_RUNNING {
		t.Errorf("expected RUNNING, got %v", actor.GetStatus())
	}
}

func TestDeleteActor_Success(t *testing.T) {
	ns := namespaceForTest("ns-delete-success")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	_, err = tc.client.DeleteActor(context.Background(), &ateapipb.DeleteActorRequest{
		ActorId: "id1",
	})
	if err != nil {
		t.Fatalf("DeleteActor failed: %v", err)
	}

	_, err = tc.client.GetActor(context.Background(), &ateapipb.GetActorRequest{
		ActorId: "id1",
	})
	assertGrpcError(t, err, codes.NotFound, "Actor id1 not found")
}

func TestDeleteActor_NotSuspended(t *testing.T) {
	ns := namespaceForTest("ns-delete-notsuspended")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	createTemplate(t, tc, ns)
	setNodeCapacity(tc, "node1", ns, "pool1", 10)

	_, err := tc.client.CreateActor(context.Background(), &ateapipb.CreateActorRequest{
		ActorTemplateNamespace: ns,
		ActorTemplateName:      "tmpl1",
		ActorId:                "id1",
	})
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	_, err = tc.client.ResumeActor(context.Background(), &ateapipb.ResumeActorRequest{
		ActorId: "id1",
	})
	if err != nil {
		t.Fatalf("ResumeActor failed: %v", err)
	}

	_, err = tc.client.DeleteActor(context.Background(), &ateapipb.DeleteActorRequest{
		ActorId: "id1",
	})
	assertGrpcError(t, err, codes.FailedPrecondition, "Actor id1 is not suspended (status: STATUS_RUNNING)")
}

func TestDeleteActor_NotFound(t *testing.T) {
	ns := namespaceForTest("ns-delete-notfound")
	tc := setupTest(t, ns)
	defer tc.cleanup()

	_, err := tc.client.DeleteActor(context.Background(), &ateapipb.DeleteActorRequest{
		ActorId: "non-existent",
	})
	assertGrpcError(t, err, codes.NotFound, "Actor non-existent not found")
}

func assertGrpcError(t *testing.T, err error, wantCode codes.Code, wantMsg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != wantCode {
		t.Errorf("expected status %v, got %v", wantCode, st.Code())
	}
	if st.Message() != wantMsg {
		t.Errorf("expected message %q, got %q", wantMsg, st.Message())
	}
}
