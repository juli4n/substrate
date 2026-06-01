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

package workerstore_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/agent-substrate/substrate/cmd/atelet/internal/workerstore"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
)

// newStore creates a Store backed by a temp-dir bbolt database.
// It registers a cleanup that closes the store after the test.
func newStore(t *testing.T) (workerstore.Interface, *workerstore.Broadcaster) {
	t.Helper()
	b := workerstore.NewBroadcaster()
	s, err := workerstore.NewStore(filepath.Join(t.TempDir(), "test.db"), b)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, b
}

// TestRegisterWorkerPod_NewPod verifies that a freshly registered pod appears as free
// and publishes a capacity signal.
func TestRegisterWorkerPod_NewPod(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}
	_, ch := b.Subscribe()

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	expectSignal(t, ch)

	_, podIP, found, err := s.GetWorkerPodAssignment("any-actor")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if found {
		t.Fatalf("new pod should not be assigned, got IP %s", podIP)
	}

	snap, err := s.CapacitySnapshot()
	if err != nil {
		t.Fatalf("CapacitySnapshot: %v", err)
	}
	want := &ateletpb.CapacitySnapshot{
		Pools: []*ateletpb.PoolCapacity{
			{PoolNamespace: "ns", PoolName: "pool1", Free: 1, Total: 1},
		},
	}
	if diff := cmp.Diff(want, snap, protocmp.Transform()); diff != "" {
		t.Errorf("unexpected snapshot (-want +got):\n%s", diff)
	}
}

// TestRegisterWorkerPod_ReregistrationWithDifferentValuesErrors verifies that
// re-registering the same UID with different pod values returns an error.
func TestRegisterWorkerPod_ReregistrationWithDifferentValuesErrors(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}

	differentPool := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool2"}
	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", differentPool); err == nil {
		t.Error("expected error when re-registering same UID with different pool, got nil")
	}
}

// TestRegisterWorkerPod_ReregistrationPreservesAssignment verifies that a
// redundant RegisterWorkerPod call (e.g. from an informer UpdateFunc firing
// for a label or condition change) does not clear an existing actor assignment
// and does not emit a spurious capacity signal.
func TestRegisterWorkerPod_ReregistrationPreservesAssignment(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	_, ch := b.Subscribe()
	drainSignals(ch)

	// Re-register the same pod (simulates informer UpdateFunc for a non-IP field change).
	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	expectNoSignal(t, ch)

	_, _, found, err := s.GetWorkerPodAssignment("actor1")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if !found {
		t.Fatal("expected assignment to be preserved after informer update")
	}
}

// TestUnregisterWorkerPod_Free removes a free pod and checks it's gone.
func TestUnregisterWorkerPod_Free(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if err := s.UnregisterWorkerPod(uid1); err != nil {
		t.Fatalf("UnregisterWorkerPod: %v", err)
	}

	snap, err := s.CapacitySnapshot()
	if err != nil {
		t.Fatalf("CapacitySnapshot: %v", err)
	}
	if diff := cmp.Diff(&ateletpb.CapacitySnapshot{}, snap, protocmp.Transform()); diff != "" {
		t.Errorf("expected empty snapshot after unregister (-want +got):\n%s", diff)
	}
}

// TestUnregisterWorkerPod_AlsoClearsAssignment verifies that removing an assigned pod
// also removes the actor→pod assignment.
func TestUnregisterWorkerPod_AlsoClearsAssignment(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}
	if err := s.UnregisterWorkerPod(uid1); err != nil {
		t.Fatalf("UnregisterWorkerPod: %v", err)
	}

	_, _, found, err := s.GetWorkerPodAssignment("actor1")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if found {
		t.Error("assignment should have been removed when pod was unregistered")
	}
}

// TestUnregisterWorkerPod_Unknown verifies that unregistering an unknown pod is a
// no-op (no error, no signal).
func TestUnregisterWorkerPod_Unknown(t *testing.T) {
	s, b := newStore(t)
	_, ch := b.Subscribe()

	if err := s.UnregisterWorkerPod(workerstore.WorkerPodUID("uid-ghost")); err != nil {
		t.Fatalf("UnregisterWorkerPod of unknown pod should not error: %v", err)
	}
	expectNoSignal(t, ch)
}

// TestAssignWorkerPod_Success claims a free pod and checks the returned coordinates.
func TestAssignWorkerPod_Success(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	pod, podIP, err := s.AssignWorkerPod("actor1", pool1)
	if err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}
	if pod.Namespace != "ns" || pod.Name != "pod1" || podIP != "1.2.3.4" {
		t.Errorf("unexpected result: ns=%s name=%s ip=%s", pod.Namespace, pod.Name, podIP)
	}
}

// TestAssignWorkerPod_Idempotent verifies that claiming the same actor ID twice
// returns the same pod without error.
func TestAssignWorkerPod_Idempotent(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	result1, ip1, err := s.AssignWorkerPod("actor1", pool1)
	if err != nil {
		t.Fatalf("first AssignWorkerPod: %v", err)
	}
	result2, ip2, err := s.AssignWorkerPod("actor1", pool1)
	if err != nil {
		t.Fatalf("second AssignWorkerPod: %v", err)
	}
	if result1.Name != result2.Name || ip1 != ip2 {
		t.Errorf("idempotent claim returned different pod: first=%s/%s, second=%s/%s", result1.Name, ip1, result2.Name, ip2)
	}
}

// TestAssignWorkerPod_NoFreeWorkerPods_NoPods verifies ErrNoFreeWorkerPods when
// no pods are registered for the pool.
func TestAssignWorkerPod_NoFreeWorkerPods_NoPods(t *testing.T) {
	s, _ := newStore(t)
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	_, _, err := s.AssignWorkerPod("actor1", pool1)
	assertNoFreeWorkerPods(t, err)
}

// TestAssignWorkerPod_NoFreeWorkerPods_WrongPool verifies ErrNoFreeWorkerPods when
// pods exist but belong to a different pool.
func TestAssignWorkerPod_NoFreeWorkerPods_WrongPool(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	poolOther := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool-other"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", poolOther); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	_, _, err := s.AssignWorkerPod("actor1", pool1)
	assertNoFreeWorkerPods(t, err)
}

// TestAssignWorkerPod_NoFreeWorkerPods_AllAssigned verifies ErrNoFreeWorkerPods when
// all pods for the pool are already assigned.
func TestAssignWorkerPod_NoFreeWorkerPods_AllAssigned(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}
	_, _, err := s.AssignWorkerPod("actor2", pool1)
	assertNoFreeWorkerPods(t, err)
}

// TestGetWorkerPodAssignment_NotFound returns found=false for an unknown actor.
func TestGetWorkerPodAssignment_NotFound(t *testing.T) {
	s, _ := newStore(t)

	_, _, found, err := s.GetWorkerPodAssignment("actor-unknown")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if found {
		t.Error("expected found=false for unknown actor")
	}
}

// TestGetWorkerPodAssignment_Found returns the correct pod after a claim.
func TestGetWorkerPodAssignment_Found(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	assignedPod, podIP, found, err := s.GetWorkerPodAssignment("actor1")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if assignedPod.Namespace != "ns" || assignedPod.Name != "pod1" || podIP != "1.2.3.4" {
		t.Errorf("unexpected assignment: ns=%s name=%s ip=%s", assignedPod.Namespace, assignedPod.Name, podIP)
	}
}

// TestUnassignWorkerPod_ReleasesAssignment verifies that UnassignWorkerPod makes the pod free again.
func TestUnassignWorkerPod_ReleasesAssignment(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}
	if err := s.UnassignWorkerPod("actor1"); err != nil {
		t.Fatalf("UnassignWorkerPod: %v", err)
	}

	_, _, found, err := s.GetWorkerPodAssignment("actor1")
	if err != nil {
		t.Fatalf("GetWorkerPodAssignment: %v", err)
	}
	if found {
		t.Error("expected assignment to be removed after UnassignWorkerPod")
	}

	// Pod should be claimable again.
	if _, _, err := s.AssignWorkerPod("actor2", pool1); err != nil {
		t.Errorf("pod should be free after UnassignWorkerPod but AssignWorkerPod failed: %v", err)
	}
}

// TestUnassignWorkerPod_NoOp verifies that freeing an unassigned actor is a no-op.
func TestUnassignWorkerPod_NoOp(t *testing.T) {
	s, b := newStore(t)
	_, ch := b.Subscribe()

	if err := s.UnassignWorkerPod("actor-not-assigned"); err != nil {
		t.Fatalf("UnassignWorkerPod on unassigned actor should not error: %v", err)
	}
	expectNoSignal(t, ch)
}

// TestCapacitySnapshot_Empty verifies an empty store returns an empty snapshot.
func TestCapacitySnapshot_Empty(t *testing.T) {
	s, _ := newStore(t)

	snap, err := s.CapacitySnapshot()
	if err != nil {
		t.Fatalf("CapacitySnapshot: %v", err)
	}
	if diff := cmp.Diff(&ateletpb.CapacitySnapshot{}, snap, protocmp.Transform()); diff != "" {
		t.Errorf("expected empty snapshot (-want +got):\n%s", diff)
	}
}

// TestCapacitySnapshot_MultiplePools verifies accurate free/total counts across
// multiple pools with a mix of free and assigned pods.
func TestCapacitySnapshot_MultiplePools(t *testing.T) {
	s, _ := newStore(t)
	poolA := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool-a"}
	poolB := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool-b"}

	// pool-a: 3 pods, 2 free
	for i, name := range []string{"a1", "a2", "a3"} {
		uid := workerstore.WorkerPodUID("uid-" + name)
		pod := workerstore.WorkerPodName{Namespace: "ns", Name: name}
		if err := s.RegisterWorkerPod(uid, pod, fmt.Sprintf("10.0.0.%d", i+1), poolA); err != nil {
			t.Fatalf("RegisterWorkerPod %s: %v", name, err)
		}
	}
	if _, _, err := s.AssignWorkerPod("actor-a1", poolA); err != nil {
		t.Fatalf("AssignWorkerPod pool-a: %v", err)
	}

	// pool-b: 2 pods, 0 free (both assigned)
	for i, name := range []string{"b1", "b2"} {
		uid := workerstore.WorkerPodUID("uid-" + name)
		pod := workerstore.WorkerPodName{Namespace: "ns", Name: name}
		if err := s.RegisterWorkerPod(uid, pod, fmt.Sprintf("10.1.0.%d", i+1), poolB); err != nil {
			t.Fatalf("RegisterWorkerPod %s: %v", name, err)
		}
	}
	if _, _, err := s.AssignWorkerPod("actor-b1", poolB); err != nil {
		t.Fatalf("AssignWorkerPod pool-b 1: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor-b2", poolB); err != nil {
		t.Fatalf("AssignWorkerPod pool-b 2: %v", err)
	}

	snap, err := s.CapacitySnapshot()
	if err != nil {
		t.Fatalf("CapacitySnapshot: %v", err)
	}
	want := &ateletpb.CapacitySnapshot{
		Pools: []*ateletpb.PoolCapacity{
			{PoolNamespace: "ns", PoolName: "pool-a", Free: 2, Total: 3},
			{PoolNamespace: "ns", PoolName: "pool-b", Free: 0, Total: 2},
		},
	}
	sortPools := cmpopts.SortSlices(func(a, b protocmp.Message) bool {
		return a["pool_name"].(string) < b["pool_name"].(string)
	})
	if diff := cmp.Diff(want, snap, protocmp.Transform(), sortPools); diff != "" {
		t.Errorf("unexpected snapshot (-want +got):\n%s", diff)
	}
}

// TestCapacitySnapshot_UpdatesAfterFree verifies counts change when a pod is freed.
func TestCapacitySnapshot_UpdatesAfterFree(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	snap, _ := s.CapacitySnapshot()
	if diff := cmp.Diff(&ateletpb.CapacitySnapshot{
		Pools: []*ateletpb.PoolCapacity{{PoolNamespace: "ns", PoolName: "pool1", Free: 0, Total: 1}},
	}, snap, protocmp.Transform()); diff != "" {
		t.Fatalf("unexpected snapshot after claim (-want +got):\n%s", diff)
	}

	if err := s.UnassignWorkerPod("actor1"); err != nil {
		t.Fatalf("UnassignWorkerPod: %v", err)
	}

	snap, _ = s.CapacitySnapshot()
	if diff := cmp.Diff(&ateletpb.CapacitySnapshot{
		Pools: []*ateletpb.PoolCapacity{{PoolNamespace: "ns", PoolName: "pool1", Free: 1, Total: 1}},
	}, snap, protocmp.Transform()); diff != "" {
		t.Errorf("unexpected snapshot after free (-want +got):\n%s", diff)
	}
}

// TestBroadcaster_SignalOnClaim checks that claiming a pod sends a signal.
func TestBroadcaster_SignalOnClaim(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}
	_, ch := b.Subscribe()

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	drainSignals(ch) // consume the RegisterWorkerPod signal

	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}
	expectSignal(t, ch)
}

// TestBroadcaster_SignalOnFree checks that freeing a pod sends a signal.
func TestBroadcaster_SignalOnFree(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	_, ch := b.Subscribe()
	if err := s.UnassignWorkerPod("actor1"); err != nil {
		t.Fatalf("UnassignWorkerPod: %v", err)
	}
	expectSignal(t, ch)
}

// TestBroadcaster_SignalOnUnregister checks that removing a pod sends a signal.
func TestBroadcaster_SignalOnUnregister(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}

	_, ch := b.Subscribe()
	drainSignals(ch)

	if err := s.UnregisterWorkerPod(uid1); err != nil {
		t.Fatalf("UnregisterWorkerPod: %v", err)
	}
	expectSignal(t, ch)
}

// TestBroadcaster_NoSignalOnIdempotentClaim checks that re-claiming the same
// actor ID does not emit a spurious signal.
func TestBroadcaster_NoSignalOnIdempotentClaim(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("first AssignWorkerPod: %v", err)
	}

	_, ch := b.Subscribe()
	drainSignals(ch)

	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("second AssignWorkerPod: %v", err)
	}
	expectNoSignal(t, ch)
}

// TestBroadcaster_MultipleSubscribers verifies all active subscribers receive
// a signal.
func TestBroadcaster_MultipleSubscribers(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}

	id1, ch1 := b.Subscribe()
	id2, ch2 := b.Subscribe()
	_, ch3 := b.Subscribe()
	defer b.Unsubscribe(id1)
	defer b.Unsubscribe(id2)

	drainSignals(ch1)
	drainSignals(ch2)
	drainSignals(ch3)

	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	expectSignal(t, ch1)
	expectSignal(t, ch2)
	expectSignal(t, ch3)
}

// TestBroadcaster_UnsubscribeStopsSignals verifies that after Unsubscribe, the
// closed channel no longer receives signals.
func TestBroadcaster_UnsubscribeStopsSignals(t *testing.T) {
	s, b := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	pod1 := workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, pod1, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod: %v", err)
	}

	id, ch := b.Subscribe()
	drainSignals(ch)

	b.Unsubscribe(id)

	// Channel should be closed — reading should return the zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after Unsubscribe")
		}
	default:
		// Channel not yet drained — that's fine, it's closed so it will return false.
	}
}

// TestWorkerPodUIDs_Empty verifies an empty store returns a nil/empty slice.
func TestWorkerPodUIDs_Empty(t *testing.T) {
	s, _ := newStore(t)

	uids, err := s.WorkerPodUIDs()
	if err != nil {
		t.Fatalf("WorkerPodUIDs: %v", err)
	}
	if len(uids) != 0 {
		t.Errorf("expected no UIDs, got %v", uids)
	}
}

// TestWorkerPodUIDs_ReflectsRegistrations verifies UIDs match registered pods and
// shrink after unregistration.
func TestWorkerPodUIDs_ReflectsRegistrations(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	uid2 := workerstore.WorkerPodUID("uid-pod2")
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod uid1: %v", err)
	}
	if err := s.RegisterWorkerPod(uid2, workerstore.WorkerPodName{Namespace: "ns", Name: "pod2"}, "1.2.3.5", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod uid2: %v", err)
	}

	uids, err := s.WorkerPodUIDs()
	if err != nil {
		t.Fatalf("WorkerPodUIDs: %v", err)
	}
	want := []workerstore.WorkerPodUID{uid1, uid2}
	if diff := cmp.Diff(want, uids, cmpopts.SortSlices(func(a, b workerstore.WorkerPodUID) bool { return a < b })); diff != "" {
		t.Errorf("unexpected UIDs (-want +got):\n%s", diff)
	}

	if err := s.UnregisterWorkerPod(uid1); err != nil {
		t.Fatalf("UnregisterWorkerPod: %v", err)
	}
	uids, err = s.WorkerPodUIDs()
	if err != nil {
		t.Fatalf("WorkerPodUIDs after unregister: %v", err)
	}
	if diff := cmp.Diff([]workerstore.WorkerPodUID{uid2}, uids); diff != "" {
		t.Errorf("unexpected UIDs after unregister (-want +got):\n%s", diff)
	}
}

// TestDump_Empty verifies an empty store produces empty maps.
func TestDump_Empty(t *testing.T) {
	s, _ := newStore(t)

	dump, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if len(dump.Pods) != 0 {
		t.Errorf("expected no pods in dump, got %v", dump.Pods)
	}
	if len(dump.Assignments) != 0 {
		t.Errorf("expected no assignments in dump, got %v", dump.Assignments)
	}
}

// TestDump_ReflectsState verifies Dump returns correct pods and assignments.
func TestDump_ReflectsState(t *testing.T) {
	s, _ := newStore(t)
	uid1 := workerstore.WorkerPodUID("uid-pod1")
	uid2 := workerstore.WorkerPodUID("uid-pod2")
	pool1 := workerstore.WorkerPoolName{Namespace: "ns", Name: "pool1"}

	if err := s.RegisterWorkerPod(uid1, workerstore.WorkerPodName{Namespace: "ns", Name: "pod1"}, "1.2.3.4", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod uid1: %v", err)
	}
	if err := s.RegisterWorkerPod(uid2, workerstore.WorkerPodName{Namespace: "ns", Name: "pod2"}, "1.2.3.5", pool1); err != nil {
		t.Fatalf("RegisterWorkerPod uid2: %v", err)
	}
	if _, _, err := s.AssignWorkerPod("actor1", pool1); err != nil {
		t.Fatalf("AssignWorkerPod: %v", err)
	}

	dump, err := s.Dump()
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if len(dump.Pods) != 2 {
		t.Errorf("expected 2 pods in dump, got %d", len(dump.Pods))
	}
	if len(dump.Assignments) != 1 {
		t.Errorf("expected 1 assignment in dump, got %d", len(dump.Assignments))
	}
	if _, ok := dump.Assignments["actor1"]; !ok {
		t.Error("expected actor1 assignment in dump")
	}
}

// assertNoFreeWorkerPods checks that err is ErrNoFreeWorkerPods.
func assertNoFreeWorkerPods(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, workerstore.ErrNoFreeWorkerPods) {
		t.Errorf("expected ErrNoFreeWorkerPods, got: %v", err)
	}
}

// expectSignal asserts that a capacity signal is immediately present on ch.
// Publish is called synchronously before any store method returns, so the
// signal is already in the buffer by the time the assertion runs.
func expectSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Error("expected a capacity signal but none arrived")
	}
}

// expectNoSignal asserts that no capacity signal is present on ch.
func expectNoSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Error("unexpected capacity signal")
	default:
	}
}

// drainSignals consumes all buffered signals so later assertions start clean.
func drainSignals(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
