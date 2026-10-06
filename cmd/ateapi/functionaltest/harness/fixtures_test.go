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

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
)

func TestFixtures(t *testing.T) {
	t.Parallel()
	h := New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"), WithActorCapacity(4))
	tmpl := h.NewTemplate(as, pool, WithExternalVolume("data", "/data"))

	running := h.RunningActor(as, tmpl)
	if got := running.GetStatus().GetState(); got != ateapipb.ActorState_ACTOR_STATE_RUNNING {
		t.Errorf("RunningActor is %v, want RUNNING", got)
	}
	h.WorkerOf(running)

	suspended := h.SuspendedActor(as, tmpl)
	if got := suspended.GetStatus().GetState(); got != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		t.Errorf("SuspendedActor is %v, want SUSPENDED", got)
	}
	if names, _ := h.ObjectStore.Snapshot(suspended.GetStatus().GetExternalSnapshot().GetSnapshotUri()); len(names) == 0 {
		t.Error("SuspendedActor holds no external snapshot in object storage")
	}

	paused := h.PausedActor(as, tmpl)
	if got := paused.GetStatus().GetState(); got != ateapipb.ActorState_ACTOR_STATE_PAUSED {
		t.Errorf("PausedActor is %v, want PAUSED", got)
	}

	crashed := h.CrashedActor(as, tmpl)
	if crashed.GetStatus().GetWorkerAssignment() != nil {
		t.Errorf("CrashedActor holds worker assignment %v, want none", crashed.GetStatus().GetWorkerAssignment())
	}
}
