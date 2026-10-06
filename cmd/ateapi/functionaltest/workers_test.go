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

package functionaltest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agent-substrate/substrate/cmd/ateapi/functionaltest/harness"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// workerSpec is a valid Worker named after the pod UID uid.
func workerSpec(uid string) *ateapipb.Worker {
	return &ateapipb.Worker{
		Metadata:        &ateapipb.ResourceMetadata{Name: uid},
		WorkerNamespace: "workers",
		WorkerPool:      "pool1",
		WorkerPod:       "worker-" + uid[:8],
		WorkerPodUid:    uid,
		NodeName:        "node-a",
		Ips:             []string{"10.1.2.3"},
		SandboxClass:    "gvisor",
	}
}

const workerUID = "5f2c1a90-7b34-4e6d-8a11-0c3e9d5b7f42"

func TestCreateWorker(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	created, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	want := &ateapipb.Worker{
		Metadata:        &ateapipb.ResourceMetadata{Name: workerUID},
		WorkerNamespace: "workers",
		WorkerPool:      "pool1",
		WorkerPod:       "worker-5f2c1a90",
		WorkerPodUid:    workerUID,
		NodeName:        "node-a",
		Ips:             []string{"10.1.2.3"},
		SandboxClass:    "gvisor",
		Status:          &ateapipb.WorkerStatus{State: ateapipb.WorkerState_WORKER_STATE_ACTIVE},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created worker (-want +got):\n%s", diff)
	}
}

func TestCreateWorker_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	_, err = h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateWorker of an existing name = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("worker after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateWorker_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	worker := workerSpec(workerUID)
	worker.Metadata.Name = "Not_A_Valid_Name"

	_, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: worker})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateWorker with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.metadata.name") {
		t.Errorf("CreateWorker error %q does not name the invalid field worker.metadata.name", msg)
	}
}

func TestGetWorker(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetWorker (-created +got):\n%s", diff)
	}
}

func TestGetWorker_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetWorker of a missing worker = %v, want NotFound", err)
	}
}

func TestGetWorker_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetWorker with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.name") {
		t.Errorf("GetWorker error %q does not name the invalid field worker.name", msg)
	}
}

func TestListWorkers(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	const n, pageSize = 5, 2
	var created []*ateapipb.Worker
	for i := range n {
		worker, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(fmt.Sprintf("5f2c1a90-7b34-4e6d-8a11-0c3e9d5b7f4%d", i))})
		if err != nil {
			t.Fatalf("CreateWorker: %v", err)
		}
		created = append(created, worker)
	}

	var listed []*ateapipb.Worker
	req := &ateapipb.ListWorkersRequest{PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListWorkers(t.Context(), req)
		if err != nil {
			t.Fatalf("ListWorkers page %d: %v", pages, err)
		}
		if got := len(resp.GetWorkers()); got > pageSize {
			t.Errorf("page %d holds %d workers, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetWorkers()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byName := cmpopts.SortSlices(func(a, b *ateapipb.Worker) bool { return a.GetMetadata().GetName() < b.GetMetadata().GetName() })
	if diff := cmp.Diff(created, listed, protocmp.Transform(), byName); diff != "" {
		t.Errorf("listed workers (-want +got):\n%s", diff)
	}
}

func TestListWorkers_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListWorkers(t.Context(), &ateapipb.ListWorkersRequest{PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListWorkers with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListWorkers error %q does not name the invalid field page_size", msg)
	}
}

func TestListWorkers_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListWorkers(t.Context(), &ateapipb.ListWorkersRequest{PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListWorkers with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestUpdateWorker(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: workerUID}
	before, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	toUpdate := proto.Clone(before).(*ateapipb.Worker)
	toUpdate.Labels = map[string]string{"tier": "1"}
	updated, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: toUpdate})
	if err != nil {
		t.Fatalf("UpdateWorker: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.Worker)
	want.Labels = map[string]string{"tier": "1"}
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated worker (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated worker has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetWorker after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateWorker_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	worker := workerSpec(workerUID)
	worker.Metadata.Uid, worker.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: worker})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateWorker of a missing worker = %v, want NotFound", err)
	}
}

func TestUpdateWorker_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: workerUID}
	before, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.Worker)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.Labels = map[string]string{"tier": "1"}
		_, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateWorker with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateWorker with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("worker after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateWorker without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateWorker_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	worker := workerSpec(workerUID)
	worker.Metadata.Name = "Not_A_Valid_Name"
	worker.Metadata.Uid, worker.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: worker})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateWorker with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.metadata.name") {
		t.Errorf("UpdateWorker error %q does not name the invalid field worker.metadata.name", msg)
	}
}

func TestUpdateWorker_ImmutableField(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), h.Node("a"))
	before := h.GetWorker(w)

	toUpdate := proto.Clone(before).(*ateapipb.Worker)
	toUpdate.NodeName = "another-node"
	_, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: toUpdate})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateWorker changing the node name = %v, want InvalidArgument", err)
	}
	if diff := cmp.Diff(before, h.GetWorker(w), protocmp.Transform()); diff != "" {
		t.Errorf("worker after a refused update (-want +got):\n%s", diff)
	}
}

func TestDeleteWorker(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: workerUID}
	created, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	deleted, err := h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("DeleteWorker: %v", err)
	}
	want := proto.Clone(created).(*ateapipb.Worker)
	want.Status.State = ateapipb.WorkerState_WORKER_STATE_DRAINING
	if diff := cmp.Diff(want, deleted, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("DeleteWorker response (-want +got):\n%s", diff)
	}
	if _, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetWorker after delete = %v, want NotFound", err)
	}
}

func TestDeleteWorker_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteWorker of a missing worker = %v, want NotFound", err)
	}
}

func TestDeleteWorker_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: workerUID}
	created, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()

	_, err = h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: ref, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteWorker with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteWorker with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("GetWorker after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("worker after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteWorker with the right uid and version: %v", err)
	}
}

func TestDeleteWorker_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteWorker with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.name") {
		t.Errorf("DeleteWorker error %q does not name the invalid field worker.name", msg)
	}
}

func TestDeleteWorker_HostingActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.RunningActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	host := h.WorkerOf(actor)
	before := h.GetActor(ref)

	if _, err := h.Control.DeleteWorker(t.Context(), &ateapipb.DeleteWorkerRequest{Worker: host.Ref()}); err != nil {
		t.Fatalf("DeleteWorker: %v", err)
	}
	if _, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: host.Ref()}); status.Code(err) != codes.NotFound {
		t.Errorf("GetWorker after delete = %v, want NotFound", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	want.Status.WorkerAssignment = nil
	want.Status.Crash = &ateapipb.ActorCrash{Message: "worker pod went away while hosting the actor"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after its worker was deleted (-want +got):\n%s", diff)
	}
}

func TestDrainWorker(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: workerUID}
	before, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)})
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	drained, err := h.Control.DrainWorker(t.Context(), &ateapipb.DrainWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("DrainWorker: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.Worker)
	want.Status.State = ateapipb.WorkerState_WORKER_STATE_DRAINING
	if diff := cmp.Diff(want, drained, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("drained worker (-want +got):\n%s", diff)
	}
	if got, was := drained.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("drained worker has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetWorker(t.Context(), &ateapipb.GetWorkerRequest{Worker: ref})
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(drained, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetWorker after drain (-drained +got):\n%s", diff)
	}
}

func TestDrainWorker_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DrainWorker(t.Context(), &ateapipb.DrainWorkerRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DrainWorker of a missing worker = %v, want NotFound", err)
	}
}

func TestDrainWorker_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DrainWorker(t.Context(), &ateapipb.DrainWorkerRequest{Worker: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DrainWorker with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.name") {
		t.Errorf("DrainWorker error %q does not name the invalid field worker.name", msg)
	}
}

func TestDrainWorker_AlreadyDraining(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), h.Node("a"))
	w.Drain()
	before := h.GetWorker(w)

	drained, err := h.Control.DrainWorker(t.Context(), &ateapipb.DrainWorkerRequest{Worker: w.Ref()})
	if status.Code(err) != codes.OK {
		t.Fatalf("DrainWorker of a draining worker = %v, want OK", err)
	}
	if diff := cmp.Diff(before, drained, protocmp.Transform()); diff != "" {
		t.Errorf("DrainWorker response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, h.GetWorker(w), protocmp.Transform()); diff != "" {
		t.Errorf("worker after draining it again (-want +got):\n%s", diff)
	}
}

func TestListWorkerActorAssignments(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	const n, pageSize = 3, 2
	w := h.AddWorker(pool, h.Node("a"), harness.WithActorCapacity(n))
	tmpl := h.NewTemplate(as, pool)
	var want []*ateapipb.ActorAssignment
	for range n {
		actor := h.RunningActor(as, tmpl)
		want = append(want, &ateapipb.ActorAssignment{
			Metadata:         &ateapipb.ResourceMetadata{Name: actor.GetMetadata().GetUid()},
			Actor:            harness.ActorRef(actor),
			ActorUid:         actor.GetMetadata().GetUid(),
			ActorTemplateRef: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
		})
	}

	var listed []*ateapipb.ActorAssignment
	req := &ateapipb.ListWorkerActorAssignmentsRequest{Worker: w.Ref(), PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListWorkerActorAssignments(t.Context(), req)
		if err != nil {
			t.Fatalf("ListWorkerActorAssignments page %d: %v", pages, err)
		}
		if got := len(resp.GetActorAssignments()); got > pageSize {
			t.Errorf("page %d holds %d assignments, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetActorAssignments()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byActor := cmpopts.SortSlices(func(a, b *ateapipb.ActorAssignment) bool { return a.GetActorUid() < b.GetActorUid() })
	if diff := cmp.Diff(want, listed, protocmp.Transform(), ignoreServerMetadata, byActor); diff != "" {
		t.Errorf("listed assignments (-want +got):\n%s", diff)
	}
}

func TestListWorkerActorAssignments_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ListWorkerActorAssignments of a missing worker = %v, want NotFound", err)
	}
}

func TestListWorkerActorAssignments_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}, PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListWorkerActorAssignments with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListWorkerActorAssignments error %q does not name the invalid field page_size", msg)
	}
}

func TestListWorkerActorAssignments_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	if _, err := h.Control.CreateWorker(t.Context(), &ateapipb.CreateWorkerRequest{Worker: workerSpec(workerUID)}); err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	_, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: &ateapipb.ObjectRef{Name: workerUID}, PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListWorkerActorAssignments with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestSetWorkerCapacity(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	node := h.Node("a")
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), node)
	report := func(capacity *ateapipb.WorkerResources) *ateapipb.Worker {
		t.Helper()
		resp, err := node.WorkerService().SetWorkerCapacity(t.Context(), &ateapipb.SetWorkerCapacityRequest{Worker: w.Ref(), Capacity: capacity})
		if err != nil {
			t.Fatalf("SetWorkerCapacity: %v", err)
		}
		return resp.GetWorker()
	}
	assertReported := func(before, reported *ateapipb.Worker, capacity *ateapipb.WorkerResources) {
		t.Helper()
		want := proto.Clone(before).(*ateapipb.Worker)
		want.Status.Capacity = capacity
		if diff := cmp.Diff(want, reported, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
			t.Errorf("worker after reporting %v (-want +got):\n%s", capacity, diff)
		}
		if got, was := reported.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
			t.Errorf("worker after reporting %v has version %d, want higher than %d", capacity, got, was)
		}
		if diff := cmp.Diff(reported, h.GetWorker(w), protocmp.Transform()); diff != "" {
			t.Errorf("GetWorker after reporting %v (-reported +got):\n%s", capacity, diff)
		}
	}

	before := h.GetWorker(w)
	withResources := &ateapipb.WorkerResources{Actors: 3, Resources: &ateapipb.Resources{Limits: []*ateapipb.Limits{
		{Name: "cpu", Quantity: "2"},
		{Name: "memory", Quantity: "4Gi"},
	}}}
	reported := report(withResources)
	assertReported(before, reported, withResources)

	slotsOnly := &ateapipb.WorkerResources{Actors: 3}
	assertReported(reported, report(slotsOnly), slotsOnly)
}

func TestSetWorkerCapacity_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	node := h.Node("a")

	_, err := node.WorkerService().SetWorkerCapacity(t.Context(), &ateapipb.SetWorkerCapacityRequest{
		Worker:   &ateapipb.ObjectRef{Name: workerUID},
		Capacity: &ateapipb.WorkerResources{Actors: 1},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("SetWorkerCapacity of a missing worker = %v, want NotFound", err)
	}
}

func TestSetWorkerCapacity_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	node := h.Node("a")

	_, err := node.WorkerService().SetWorkerCapacity(t.Context(), &ateapipb.SetWorkerCapacityRequest{
		Worker:   &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"},
		Capacity: &ateapipb.WorkerResources{Actors: 1},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("SetWorkerCapacity with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "worker.name") {
		t.Errorf("SetWorkerCapacity error %q does not name the invalid field worker.name", msg)
	}
}

func TestSetWorkerCapacity_Unchanged(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	node := h.Node("a")
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), node)
	before := h.GetWorker(w)

	resp, err := node.WorkerService().SetWorkerCapacity(t.Context(), &ateapipb.SetWorkerCapacityRequest{
		Worker:   w.Ref(),
		Capacity: proto.Clone(before.GetStatus().GetCapacity()).(*ateapipb.WorkerResources),
	})
	if status.Code(err) != codes.OK {
		t.Fatalf("SetWorkerCapacity of the capacity the worker has = %v, want OK", err)
	}
	if diff := cmp.Diff(before, resp.GetWorker(), protocmp.Transform()); diff != "" {
		t.Errorf("SetWorkerCapacity response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, h.GetWorker(w), protocmp.Transform()); diff != "" {
		t.Errorf("worker after an unchanged report (-want +got):\n%s", diff)
	}
}

func TestSetWorkerCapacity_OtherNode(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), h.Node("a"))
	before := h.GetWorker(w)

	_, err := h.Node("b").WorkerService().SetWorkerCapacity(t.Context(), &ateapipb.SetWorkerCapacityRequest{
		Worker:   w.Ref(),
		Capacity: &ateapipb.WorkerResources{Actors: 9},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("SetWorkerCapacity as another node's atelet = %v, want NotFound", err)
	}
	if diff := cmp.Diff(before, h.GetWorker(w), protocmp.Transform()); diff != "" {
		t.Errorf("worker after a refused report (-want +got):\n%s", diff)
	}
}
