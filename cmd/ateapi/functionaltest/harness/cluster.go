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

package harness

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/agent-substrate/substrate/internal/atelet"
	"github.com/agent-substrate/substrate/internal/installdefaults"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

// ipCount hands out pod IPs unique within the process. Nothing routes to them:
// ate-api's dialer resolves an atelet's IP and the harness serves the
// connection in memory.
var ipCount atomic.Uint32

func nextPodIP() string {
	n := ipCount.Add(1)
	return fmt.Sprintf("10.%d.%d.%d", byte(n>>16), byte(n>>8), byte(n))
}

// Node is a k8s node with an atelet on it.
type Node struct {
	// Name is the node's name in the cluster, unique to the test.
	Name string
	// Atelet is the node's fake atelet.
	Atelet *Atelet

	// workerService is how the node's atelet reaches ate-api's
	// WorkerService, presenting the atelet's pod identity.
	workerService ateapipb.WorkerServiceClient
}

// WorkerService is a client of ate-api's WorkerService that authenticates as
// the node's atelet, as atelet's calls on behalf of the node's workers do.
func (n *Node) WorkerService() ateapipb.WorkerServiceClient {
	return n.workerService
}

// Node returns the node called name in this test, creating it and its atelet
// on first use.
func (h *Harness) Node(name string) *Node {
	h.t.Helper()
	h.mu.Lock()
	n, ok := h.nodesByTestName[name]
	h.mu.Unlock()
	if ok {
		return n
	}

	nodeName := h.namespace + "-" + name
	podName := "atelet-" + nodeName
	ip := nextPodIP()
	n = &Node{Name: nodeName, Atelet: newAtelet(h.ObjectStore)}

	lis := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	ateletpb.RegisterAteomHerderServer(server, n.Atelet)
	go func() { _ = server.Serve(lis) }()
	h.t.Cleanup(server.Stop)
	h.mu.Lock()
	h.ateletListenersByAddr[net.JoinHostPort(ip, strconv.Itoa(atelet.DefaultPort))] = lis
	h.mu.Unlock()

	pod := h.createRunningPod(installdefaults.SystemNamespace, podName, nodeName, ip, map[string]string{"app": "atelet"})
	h.t.Cleanup(func() {
		_ = h.k8s.CoreV1().Pods(installdefaults.SystemNamespace).Delete(context.Background(), podName, metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To[int64](0),
		})
	})

	cert, err := h.pki.ateletCert(podName, string(pod.UID), nodeName)
	if err != nil {
		h.t.Fatal(err)
	}
	n.workerService = ateapipb.NewWorkerServiceClient(h.dialAteAPI(h.pki.clientTLS(cert)))

	h.mu.Lock()
	h.nodesByTestName[name] = n
	h.mu.Unlock()
	return n
}

// WorkerPool is the shape a group of workers shares. Templates select a pool
// by its labels.
type WorkerPool struct {
	Name   string
	Labels map[string]string
}

// CreateWorkerPool describes a pool of workers. Without labels, the pool is
// labeled pool=name.
func (h *Harness) CreateWorkerPool(name string, labels map[string]string) *WorkerPool {
	if labels == nil {
		labels = map[string]string{"pool": name}
	}
	return &WorkerPool{Name: name, Labels: labels}
}

// Worker is a worker pod and the Worker ate-api records for it.
type Worker struct {
	// Name is the Worker's name, which is its pod's UID.
	Name string
	// Pod is the worker pod's name.
	Pod string
	// Node is the node the worker runs on.
	Node *Node
	// Pool is the pool the worker belongs to.
	Pool *WorkerPool
	// Namespace is the k8s namespace of the worker pod.
	Namespace string
	// IP is the worker pod's IP.
	IP string

	h *Harness
}

// Ref is the Worker's reference in the Control API.
func (w *Worker) Ref() *ateapipb.ObjectRef {
	return &ateapipb.ObjectRef{Name: w.Name}
}

// WorkerOption customizes a worker added by AddWorker.
type WorkerOption func(*workerOptions)

type workerOptions struct {
	actors int32
	limits []*ateapipb.Limits
}

// WithActorCapacity makes the worker report room for n actors. The default
// is one.
func WithActorCapacity(n int32) WorkerOption {
	return func(o *workerOptions) { o.actors = n }
}

// WithResourceCapacity makes the worker also report room for cpu and memory,
// given as Kubernetes quantities such as "2" and "4Gi". By default a worker
// reports no resources, so only templates without resource limits fit it.
func WithResourceCapacity(cpu, memory string) WorkerOption {
	return func(o *workerOptions) {
		o.limits = []*ateapipb.Limits{{Name: "cpu", Quantity: cpu}, {Name: "memory", Quantity: memory}}
	}
}

// AddWorker does what the worker syncer and the worker's atelet do for a new
// worker pod: it creates the pod, registers the Worker, and reports its
// capacity over WorkerService.
func (h *Harness) AddWorker(pool *WorkerPool, node *Node, opts ...WorkerOption) *Worker {
	h.t.Helper()
	o := workerOptions{actors: 1}
	for _, opt := range opts {
		opt(&o)
	}

	podName := fmt.Sprintf("%s-worker-%d", pool.Name, h.next())
	ip := nextPodIP()
	pod := h.createRunningPod(h.namespace, podName, node.Name, ip, map[string]string{"ate.dev/worker-pool": pool.Name})
	w := &Worker{Name: string(pod.UID), Pod: podName, Node: node, Pool: pool, Namespace: h.namespace, IP: ip, h: h}

	if _, err := h.Control.CreateWorker(h.ctx, &ateapipb.CreateWorkerRequest{Worker: &ateapipb.Worker{
		Metadata:        &ateapipb.ResourceMetadata{Name: w.Name},
		WorkerNamespace: h.namespace,
		WorkerPool:      pool.Name,
		WorkerPod:       podName,
		WorkerPodUid:    w.Name,
		Ips:             []string{ip},
		NodeName:        node.Name,
		SandboxClass:    string(atev1alpha1.SandboxClassGvisor),
		Labels:          pool.Labels,
	}}); err != nil {
		h.t.Fatalf("registering worker %s: %v", podName, err)
	}
	h.mu.Lock()
	h.workersByName[w.Name] = w
	h.mu.Unlock()

	capacity := &ateapipb.WorkerResources{Actors: o.actors}
	if o.limits != nil {
		capacity.Resources = &ateapipb.Resources{Limits: o.limits}
	}
	if _, err := node.WorkerService().SetWorkerCapacity(h.ctx, &ateapipb.SetWorkerCapacityRequest{Worker: w.Ref(), Capacity: capacity}); err != nil {
		h.t.Fatalf("reporting capacity of worker %s: %v", podName, err)
	}
	return w
}

// Drain marks the worker DRAINING, as the worker syncer does when its pod
// starts terminating.
func (w *Worker) Drain() {
	h := w.h
	h.t.Helper()
	if _, err := h.Control.DrainWorker(h.ctx, &ateapipb.DrainWorkerRequest{Worker: w.Ref()}); err != nil {
		h.t.Fatalf("draining worker %s: %v", w.Pod, err)
	}
}

// Remove deletes the worker pod and its Worker, as the worker syncer does once
// the pod is gone.
func (w *Worker) Remove() {
	h := w.h
	h.t.Helper()
	if err := h.k8s.CoreV1().Pods(h.namespace).Delete(h.ctx, w.Pod, metav1.DeleteOptions{
		GracePeriodSeconds: ptr.To[int64](0),
	}); err != nil {
		h.t.Fatalf("deleting worker pod %s: %v", w.Pod, err)
	}
	if _, err := h.Control.DeleteWorker(h.ctx, &ateapipb.DeleteWorkerRequest{Worker: w.Ref()}); err != nil {
		h.t.Fatalf("deleting worker %s: %v", w.Pod, err)
	}
}

// worker returns the Worker the test added under name, or nil.
func (h *Harness) worker(name string) *Worker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.workersByName[name]
}

// createRunningPod creates a pod on nodeName and marks it running and ready
// at ip, as the kubelet would.
func (h *Harness) createRunningPod(namespace, name, nodeName, ip string, labels map[string]string) *corev1.Pod {
	h.t.Helper()
	created, err := h.k8s.CoreV1().Pods(namespace).Create(h.ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			NodeName:   nodeName,
			Containers: []corev1.Container{{Name: "main", Image: "harness"}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		h.t.Fatalf("creating pod %s/%s: %v", namespace, name, err)
	}
	created.Status.Phase = corev1.PodRunning
	created.Status.PodIP = ip
	created.Status.PodIPs = []corev1.PodIP{{IP: ip}}
	created.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	updated, err := h.k8s.CoreV1().Pods(namespace).UpdateStatus(h.ctx, created, metav1.UpdateOptions{})
	if err != nil {
		h.t.Fatalf("updating the status of pod %s/%s: %v", namespace, name, err)
	}
	return updated
}

// Eventually polls cond until it holds, failing the test after a timeout.
// Tests use it to wait for an outcome ate-api reaches asynchronously.
func (h *Harness) Eventually(what string, cond func() bool) {
	h.t.Helper()
	if err := wait.PollUntilContextTimeout(h.ctx, 10*time.Millisecond, waitTimeout, true, func(context.Context) (bool, error) {
		return cond(), nil
	}); err != nil {
		h.t.Fatalf("waiting for %s: %v", what, err)
	}
}
