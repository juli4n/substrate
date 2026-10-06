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
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestWorker(t *testing.T) {
	t.Parallel()
	h := New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	nodeA := h.Node("a")
	if again := h.Node("a"); again != nodeA {
		t.Errorf("Node(a) returned %p the second time, want the first node %p", again, nodeA)
	}

	w := h.AddWorker(pool, nodeA, WithActorCapacity(3), WithResourceCapacity("2", "4Gi"))
	want := &ateapipb.Worker{
		Metadata:        &ateapipb.ResourceMetadata{Name: w.Name},
		WorkerNamespace: w.Namespace,
		WorkerPool:      "pool1",
		WorkerPod:       w.Pod,
		WorkerPodUid:    w.Name,
		NodeName:        nodeA.Name,
		Ips:             []string{w.IP},
		SandboxClass:    "gvisor",
		Labels:          map[string]string{"pool": "pool1"},
		Status: &ateapipb.WorkerStatus{
			State: ateapipb.WorkerState_WORKER_STATE_ACTIVE,
			Capacity: &ateapipb.WorkerResources{
				Actors: 3,
				Resources: &ateapipb.Resources{Limits: []*ateapipb.Limits{
					{Name: "cpu", Quantity: "2"},
					{Name: "memory", Quantity: "4Gi"},
				}},
			},
		},
	}
	ignoreServerMetadata := protocmp.IgnoreFields(&ateapipb.ResourceMetadata{}, "uid", "version", "create_time", "update_time")
	if diff := cmp.Diff(want, h.GetWorker(w), protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("registered worker (-want +got):\n%s", diff)
	}

	other := h.AddWorker(pool, h.Node("b"))
	if diff := cmp.Diff(&ateapipb.WorkerResources{Actors: 1}, h.GetWorker(other).GetStatus().GetCapacity(), protocmp.Transform()); diff != "" {
		t.Errorf("default capacity (-want +got):\n%s", diff)
	}

	actor := h.NewActor(as, h.NewTemplate(as, pool))
	w.Drain()
	other.Remove()
	var err error
	h.Eventually("ResumeActor to find no worker", func() bool {
		_, err = h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ActorRef(actor)})
		return status.Code(err) == codes.ResourceExhausted
	})
}
