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
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// AteletOp names an atelet RPC a fault can be injected into.
type AteletOp string

const (
	AteletRun                    AteletOp = "Run"
	AteletCheckpoint             AteletOp = "Checkpoint"
	AteletRestore                AteletOp = "Restore"
	AteletUploadPausedCheckpoint AteletOp = "UploadPausedCheckpoint"
	AteletTerminate              AteletOp = "Terminate"
)

// snapshotObjects names the objects a fake checkpoint writes. Nothing in
// ate-api reads them; it copies and deletes whatever shares a snapshot's prefix.
var snapshotObjects = []string{"manifest.json", "memory.zst"}

// Sandbox is an actor's workload running on a worker.
type Sandbox struct {
	ActorUID  string
	Atespace  string
	ActorName string
	// WorkerUID is the worker's pod UID, which is its Worker's name.
	WorkerUID string
}

// Call is one RPC an atelet received, faulted or not.
type Call struct {
	Op      AteletOp
	Request proto.Message
}

// Atelet is the fake atelet of one node. It keeps the sandboxes running on the
// node's workers and the local (pause) checkpoints on its disk, and reads and
// writes external snapshots in the test's ObjectStore. It refuses what a real
// atelet cannot do, such as checkpointing an actor that is not running here.
type Atelet struct {
	ateletpb.UnimplementedAteomHerderServer

	objectStore *ObjectStore
	faults      map[AteletOp]*Fault

	mu                       sync.Mutex
	calls                    []Call
	sandboxesByActorUID      map[string]Sandbox
	localSnapshotsByActorUID map[string][]string
}

func newAtelet(objectStore *ObjectStore) *Atelet {
	return &Atelet{
		objectStore: objectStore,
		faults: map[AteletOp]*Fault{
			AteletRun: {}, AteletCheckpoint: {}, AteletRestore: {}, AteletUploadPausedCheckpoint: {}, AteletTerminate: {},
		},
		sandboxesByActorUID:      map[string]Sandbox{},
		localSnapshotsByActorUID: map[string][]string{},
	}
}

// On returns the Fault deciding how calls to op behave.
func (a *Atelet) On(op AteletOp) *Fault {
	return a.faults[op]
}

// Sandboxes returns the sandboxes running on this node, sorted by actor UID.
func (a *Atelet) Sandboxes() []Sandbox {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Sandbox, 0, len(a.sandboxesByActorUID))
	for _, s := range a.sandboxesByActorUID {
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y Sandbox) int { return cmp.Compare(x.ActorUID, y.ActorUID) })
	return out
}

// LocalSnapshots returns the names of the local checkpoints this node holds
// for the actor.
func (a *Atelet) LocalSnapshots(actorUID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.localSnapshotsByActorUID[actorUID])
}

// Calls returns every RPC this atelet received, in order.
func (a *Atelet) Calls() []Call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *Atelet) record(op AteletOp, req proto.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, Call{Op: op, Request: proto.Clone(req)})
}

func (a *Atelet) Run(ctx context.Context, req *ateletpb.RunRequest) (*ateletpb.RunResponse, error) {
	a.record(AteletRun, req)
	out := a.faults[AteletRun].decide(ctx)
	if out.apply {
		a.mu.Lock()
		a.sandboxesByActorUID[req.GetActorUid()] = Sandbox{
			ActorUID: req.GetActorUid(), Atespace: req.GetAtespace(), ActorName: req.GetActorName(), WorkerUID: req.GetTargetAteomUid(),
		}
		a.mu.Unlock()
	}
	if out.err != nil {
		return nil, out.err
	}
	return &ateletpb.RunResponse{}, nil
}

func (a *Atelet) Checkpoint(ctx context.Context, req *ateletpb.CheckpointRequest) (*ateletpb.CheckpointResponse, error) {
	a.record(AteletCheckpoint, req)
	out := a.faults[AteletCheckpoint].decide(ctx)
	if out.apply {
		if err := a.checkpoint(req); err != nil {
			return nil, err
		}
	}
	if out.err != nil {
		return nil, out.err
	}
	return &ateletpb.CheckpointResponse{}, nil
}

func (a *Atelet) checkpoint(req *ateletpb.CheckpointRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid := req.GetActorUid()
	s, ok := a.sandboxesByActorUID[uid]
	if !ok || s.WorkerUID != req.GetTargetAteomUid() {
		return status.Errorf(codes.Unknown, "while calling ateom.CheckpointWorkload: actor %s is not running on ateom %s", uid, req.GetTargetAteomUid())
	}
	switch req.GetType() {
	case ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL:
		a.localSnapshotsByActorUID[uid] = append(a.localSnapshotsByActorUID[uid], req.GetLocalConfig().GetSnapshotName())
	case ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL:
		if err := a.objectStore.writeSnapshot(req.GetExternalConfig().GetSnapshotUri(), snapshotObjects...); err != nil {
			return status.Errorf(codes.Unknown, "while persisting the checkpoint: %v", err)
		}
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported checkpoint type %v", req.GetType())
	}
	delete(a.sandboxesByActorUID, uid)
	return nil
}

func (a *Atelet) Restore(ctx context.Context, req *ateletpb.RestoreRequest) (*ateletpb.RestoreResponse, error) {
	a.record(AteletRestore, req)
	out := a.faults[AteletRestore].decide(ctx)
	if out.apply {
		if err := a.restore(req); err != nil {
			return nil, err
		}
	}
	if out.err != nil {
		return nil, out.err
	}
	return &ateletpb.RestoreResponse{}, nil
}

func (a *Atelet) restore(req *ateletpb.RestoreRequest) error {
	uid := req.GetActorUid()
	switch req.GetType() {
	case ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL:
		name := req.GetLocalConfig().GetSnapshotName()
		if !slices.Contains(a.LocalSnapshots(uid), name) {
			return status.Errorf(codes.Unknown, "while reading the local checkpoint: %s not found for actor %s", name, uid)
		}
	case ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL:
		uri := req.GetExternalConfig().GetSnapshotUri()
		names, err := a.objectStore.Snapshot(uri)
		if err != nil {
			return status.Errorf(codes.Unknown, "while fetching the snapshot manifest: %v", err)
		}
		if len(names) == 0 {
			return status.Errorf(codes.Unknown, "while fetching the snapshot manifest: %s not found", uri)
		}
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported checkpoint type %v", req.GetType())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sandboxesByActorUID[uid] = Sandbox{
		ActorUID: uid, Atespace: req.GetAtespace(), ActorName: req.GetActorName(), WorkerUID: req.GetTargetAteomUid(),
	}
	return nil
}

func (a *Atelet) UploadPausedCheckpoint(ctx context.Context, req *ateletpb.UploadPausedCheckpointRequest) (*ateletpb.UploadPausedCheckpointResponse, error) {
	a.record(AteletUploadPausedCheckpoint, req)
	out := a.faults[AteletUploadPausedCheckpoint].decide(ctx)
	if out.apply {
		if !slices.Contains(a.LocalSnapshots(req.GetActorUid()), req.GetLocalSnapshotName()) {
			return nil, status.Errorf(codes.Unknown, "while reading the local checkpoint: %s not found for actor %s", req.GetLocalSnapshotName(), req.GetActorUid())
		}
		if err := a.objectStore.writeSnapshot(req.GetDestinationSnapshotUri(), snapshotObjects...); err != nil {
			return nil, status.Errorf(codes.Unknown, "while uploading the checkpoint: %v", err)
		}
	}
	if out.err != nil {
		return nil, out.err
	}
	return &ateletpb.UploadPausedCheckpointResponse{}, nil
}

// Terminate stops the actor's sandbox, if any, and drops its local checkpoints
// on this node. Terminating an actor that is not running succeeds.
func (a *Atelet) Terminate(ctx context.Context, req *ateletpb.TerminateRequest) (*ateletpb.TerminateResponse, error) {
	a.record(AteletTerminate, req)
	out := a.faults[AteletTerminate].decide(ctx)
	if out.apply {
		a.mu.Lock()
		delete(a.sandboxesByActorUID, req.GetActorUid())
		delete(a.localSnapshotsByActorUID, req.GetActorUid())
		a.mu.Unlock()
	}
	if out.err != nil {
		return nil, out.err
	}
	return &ateletpb.TerminateResponse{}, nil
}
