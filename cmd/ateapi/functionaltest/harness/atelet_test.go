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
	"testing"

	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"github.com/agent-substrate/substrate/internal/resources"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestAtelet(t *testing.T) {
	const (
		uid    = "0f0e0d0c-0b0a-4908-8706-050403020100"
		worker = "worker-uid"
	)
	uri, err := resources.NewActorSnapshotURI(StorageLocation, "ns", uid, "9c2f7b41-6d05-4e83-a1f7-3b8c0d5e2a94")
	if err != nil {
		t.Fatalf("NewActorSnapshotURI: %v", err)
	}
	snapshotURI := uri.String()

	t.Run("refuses to checkpoint an actor that is not running", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		_, err := a.Checkpoint(t.Context(), &ateletpb.CheckpointRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL,
			Config: &ateletpb.CheckpointRequest_LocalConfig{LocalConfig: &ateletpb.LocalCheckpointConfiguration{SnapshotName: "local-1"}},
		})
		if err == nil {
			t.Fatal("Checkpoint succeeded with no sandbox running")
		}
	})

	t.Run("refuses to restore a snapshot missing from object storage", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		_, err := a.Restore(t.Context(), &ateletpb.RestoreRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL,
			Config: &ateletpb.RestoreRequest_ExternalConfig{ExternalConfig: &ateletpb.ExternalRestoreConfiguration{SnapshotUri: snapshotURI}},
		})
		if err == nil {
			t.Fatal("Restore succeeded from a snapshot that is not in object storage")
		}
		if got := a.Sandboxes(); len(got) != 0 {
			t.Errorf("Sandboxes after a failed restore = %v, want none", got)
		}
	})

	t.Run("refuses to restore a local checkpoint it does not hold", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		_, err := a.Restore(t.Context(), &ateletpb.RestoreRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL,
			Config: &ateletpb.RestoreRequest_LocalConfig{LocalConfig: &ateletpb.LocalCheckpointConfiguration{SnapshotName: "local-1"}},
		})
		if err == nil {
			t.Fatal("Restore succeeded from a local checkpoint the node does not hold")
		}
	})

	t.Run("run, external checkpoint and restore", func(t *testing.T) {
		store := newObjectStore()
		a := newAtelet(store)
		if _, err := a.Run(t.Context(), &ateletpb.RunRequest{TargetAteomUid: worker, ActorUid: uid}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := a.Sandboxes(); len(got) != 1 || got[0].WorkerUID != worker {
			t.Fatalf("Sandboxes after Run = %v, want one on %s", got, worker)
		}
		if _, err := a.Checkpoint(t.Context(), &ateletpb.CheckpointRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL,
			Config: &ateletpb.CheckpointRequest_ExternalConfig{ExternalConfig: &ateletpb.ExternalCheckpointConfiguration{SnapshotUri: snapshotURI}},
		}); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		if got := a.Sandboxes(); len(got) != 0 {
			t.Errorf("Sandboxes after Checkpoint = %v, want none", got)
		}
		if names, _ := store.Snapshot(snapshotURI); len(names) == 0 {
			t.Errorf("Checkpoint wrote nothing to %s", snapshotURI)
		}
		if _, err := a.Restore(t.Context(), &ateletpb.RestoreRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL,
			Config: &ateletpb.RestoreRequest_ExternalConfig{ExternalConfig: &ateletpb.ExternalRestoreConfiguration{SnapshotUri: snapshotURI}},
		}); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if got := a.Sandboxes(); len(got) != 1 {
			t.Errorf("Sandboxes after Restore = %v, want one", got)
		}
		if got := len(a.Calls()); got != 3 {
			t.Errorf("recorded %d calls, want 3", got)
		}
	})

	t.Run("a call failed by a fault leaves no effect", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		a.On(AteletRun).FailNext(ErrSandboxFailed)
		if _, err := a.Run(t.Context(), &ateletpb.RunRequest{TargetAteomUid: worker, ActorUid: uid}); err != ErrSandboxFailed {
			t.Fatalf("Run = %v, want ErrSandboxFailed", err)
		}
		if got := a.Sandboxes(); len(got) != 0 {
			t.Errorf("Sandboxes after a failed Run = %v, want none", got)
		}
	})

	t.Run("terminate drops the sandbox and local checkpoints", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		if _, err := a.Run(t.Context(), &ateletpb.RunRequest{TargetAteomUid: worker, ActorUid: uid}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := a.Checkpoint(t.Context(), &ateletpb.CheckpointRequest{
			TargetAteomUid: worker, ActorUid: uid,
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL,
			Config: &ateletpb.CheckpointRequest_LocalConfig{LocalConfig: &ateletpb.LocalCheckpointConfiguration{SnapshotName: "local-1"}},
		}); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		if _, err := a.Terminate(t.Context(), &ateletpb.TerminateRequest{TargetAteomUid: worker, ActorUid: uid}); err != nil {
			t.Fatalf("Terminate: %v", err)
		}
		if got := a.LocalSnapshots(uid); len(got) != 0 {
			t.Errorf("LocalSnapshots after Terminate = %v, want none", got)
		}
	})
	run := &ateletpb.RunRequest{TargetAteomUid: worker, ActorUid: uid, Atespace: "ns", ActorName: "actor-1"}
	localCheckpoint := &ateletpb.CheckpointRequest{
		TargetAteomUid: worker, ActorUid: uid,
		Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL,
		Config: &ateletpb.CheckpointRequest_LocalConfig{LocalConfig: &ateletpb.LocalCheckpointConfiguration{SnapshotName: "local-1"}},
	}
	running := []Sandbox{{ActorUID: uid, Atespace: "ns", ActorName: "actor-1", WorkerUID: worker}}
	assertSandboxes := func(t *testing.T, a *Atelet, want []Sandbox) {
		t.Helper()
		if diff := cmp.Diff(want, a.Sandboxes(), cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("sandboxes (-want +got):\n%s", diff)
		}
	}

	t.Run("refuses to checkpoint an actor running on another worker", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		if _, err := a.Run(t.Context(), run); err != nil {
			t.Fatalf("Run: %v", err)
		}
		elsewhere := &ateletpb.CheckpointRequest{
			TargetAteomUid: "other-worker", ActorUid: uid,
			Type:   localCheckpoint.GetType(),
			Config: localCheckpoint.GetConfig(),
		}
		if _, err := a.Checkpoint(t.Context(), elsewhere); err == nil {
			t.Fatal("Checkpoint succeeded on a worker the actor does not run on")
		}
		assertSandboxes(t, a, running)
	})

	t.Run("local checkpoint and restore", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		if _, err := a.Run(t.Context(), run); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := a.Checkpoint(t.Context(), localCheckpoint); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		assertSandboxes(t, a, nil)
		if diff := cmp.Diff([]string{"local-1"}, a.LocalSnapshots(uid)); diff != "" {
			t.Errorf("local snapshots after Checkpoint (-want +got):\n%s", diff)
		}
		if _, err := a.Restore(t.Context(), &ateletpb.RestoreRequest{
			TargetAteomUid: worker, ActorUid: uid, Atespace: "ns", ActorName: "actor-1",
			Type:   ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL,
			Config: &ateletpb.RestoreRequest_LocalConfig{LocalConfig: &ateletpb.LocalCheckpointConfiguration{SnapshotName: "local-1"}},
		}); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		assertSandboxes(t, a, running)
	})

	t.Run("uploads a paused checkpoint to object storage", func(t *testing.T) {
		store := newObjectStore()
		a := newAtelet(store)
		if _, err := a.Run(t.Context(), run); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := a.Checkpoint(t.Context(), localCheckpoint); err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		if _, err := a.UploadPausedCheckpoint(t.Context(), &ateletpb.UploadPausedCheckpointRequest{
			ActorUid: uid, LocalSnapshotName: "local-1", DestinationSnapshotUri: snapshotURI,
		}); err != nil {
			t.Fatalf("UploadPausedCheckpoint: %v", err)
		}
		got, err := store.Snapshot(snapshotURI)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if diff := cmp.Diff(snapshotObjects, got); diff != "" {
			t.Errorf("uploaded snapshot (-want +got):\n%s", diff)
		}
	})

	t.Run("refuses to upload a local checkpoint it does not hold", func(t *testing.T) {
		store := newObjectStore()
		a := newAtelet(store)
		if _, err := a.UploadPausedCheckpoint(t.Context(), &ateletpb.UploadPausedCheckpointRequest{
			ActorUid: uid, LocalSnapshotName: "local-1", DestinationSnapshotUri: snapshotURI,
		}); err == nil {
			t.Fatal("UploadPausedCheckpoint succeeded from a local checkpoint the node does not hold")
		}
		if got := store.Objects(); len(got) != 0 {
			t.Errorf("objects after a refused upload = %v, want none", got)
		}
	})

	t.Run("terminating an actor that is not running succeeds", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		if _, err := a.Terminate(t.Context(), &ateletpb.TerminateRequest{TargetAteomUid: worker, ActorUid: uid}); err != nil {
			t.Errorf("Terminate of an actor that is not running = %v, want no error", err)
		}
	})

	t.Run("a blocked call takes effect only once released", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		gate := a.On(AteletRun).Block()
		done := make(chan error)
		go func() {
			_, err := a.Run(t.Context(), run)
			done <- err
		}()
		gate.Wait(t)
		assertSandboxes(t, a, nil)
		gate.Release()
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertSandboxes(t, a, running)
	})

	t.Run("records every call, faulted or not", func(t *testing.T) {
		a := newAtelet(newObjectStore())
		a.On(AteletRun).FailNext(ErrSandboxFailed)
		if _, err := a.Run(t.Context(), run); err == nil {
			t.Fatal("Run succeeded despite the fault")
		}
		if _, err := a.Run(t.Context(), run); err != nil {
			t.Fatalf("Run: %v", err)
		}
		calls := a.Calls()
		if len(calls) != 2 || calls[0].Op != AteletRun || calls[1].Op != AteletRun {
			t.Errorf("calls = %v, want two Run calls", calls)
		}
	})
}
