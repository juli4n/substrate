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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"strings"
	"testing"

	"github.com/agent-substrate/substrate/cmd/ateapi/functionaltest/harness"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// certificateSigningRequest is a valid CSR, for minting certificates.
func certificateSigningRequest(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "actor"}}, key)
	if err != nil {
		t.Fatalf("creating a CSR: %v", err)
	}
	return csr
}

// resumingActor creates an actor from tmpl and leaves it RESUMING on w: its
// Restore fails with UNAVAILABLE, which ate-api leaves for a retry.
func resumingActor(t *testing.T, h *harness.Harness, as string, tmpl *ateapipb.ActorTemplate, w *harness.Worker) *ateapipb.Actor {
	t.Helper()
	actor := h.NewActor(as, tmpl)
	w.Node.Atelet.On(harness.AteletRestore).FailNext(harness.ErrUnavailable)
	if _, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: harness.ActorRef(actor)}); err == nil {
		t.Fatal("ResumeActor succeeded despite an unavailable atelet")
	}
	resuming := h.GetActor(harness.ActorRef(actor))
	if got := resuming.GetStatus().GetState(); got != ateapipb.ActorState_ACTOR_STATE_RESUMING {
		t.Fatalf("actor is %v after an unavailable atelet, want RESUMING", got)
	}
	return resuming
}

// workerAssignment is the assignment ate-api records on an actor placed on w.
func workerAssignment(w *harness.Worker) *ateapipb.WorkerAssignment {
	return &ateapipb.WorkerAssignment{
		Worker:          w.Ref(),
		WorkerNamespace: w.Namespace,
		WorkerPool:      w.Pool.Name,
		WorkerPod:       w.Pod,
		WorkerPodUid:    w.Name,
		WorkerPodIps:    []string{w.IP},
		NodeName:        w.Node.Name,
	}
}

func TestCreateActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	golden, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: tmpl.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag()})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	templateRef := &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()}

	created, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: templateRef,
	}})
	if err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	want := &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: templateRef,
		Status: &ateapipb.ActorStatus{
			State: ateapipb.ActorState_ACTOR_STATE_SUSPENDED,
			ExternalSnapshot: &ateapipb.ExternalSnapshot{
				SnapshotUri:      golden.GetStatus().GetSnapshot().GetSnapshotUri(),
				ContentScope:     ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL,
				ActorTemplateUid: tmpl.GetMetadata().GetUid(),
			},
		},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created actor (-want +got):\n%s", diff)
	}
}

func TestCreateActor_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	templateRef := &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()}
	created, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: templateRef,
	}})
	if err != nil {
		t.Fatalf("CreateActor: %v", err)
	}

	_, err = h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: templateRef,
	}})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateActor of an existing name = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetActor(t.Context(), &ateapipb.GetActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-a"}})
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)

	_, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "Not_A_Valid_Name"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.metadata.name") {
		t.Errorf("CreateActor error %q does not name the invalid field actor.metadata.name", msg)
	}
	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{Atespace: as})
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actors after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActor_TemplateNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-missing"},
	}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateActor with a missing template = %v, want FailedPrecondition", err)
	}
	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{Atespace: as})
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actors after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActor_StorageClassNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	if _, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: &ateapipb.ActorTemplate{
		Metadata:       &ateapipb.ResourceMetadata{Atespace: as, Name: "tmpl-a"},
		SnapshotConfig: &ateapipb.SnapshotConfig{StorageLocation: harness.StorageLocation},
		SandboxConfig: &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_GVISOR,
			ConfigName:   harness.SandboxConfigName,
		},
		Containers: []*ateapipb.Container{{
			Name:         "main",
			Image:        harness.Image,
			Command:      []string{"/main"},
			VolumeMounts: []*ateapipb.VolumeMount{{Name: "data", MountPath: "/data"}},
		}},
		Volumes: []*ateapipb.Volume{{
			Name: "data",
			ExternalVolumeTemplate: &ateapipb.ExternalVolumeTemplate{
				StorageClassName: "storage-class-missing",
				Capacity:         "1Gi",
			},
		}},
		WorkerSelector: &ateapipb.Selector{MatchLabels: map[string]string{"pool": "pool1"}},
	}}); err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	_, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-a"},
	}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateActor with a missing storage class = %v, want FailedPrecondition", err)
	}
	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{Atespace: as})
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actors after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActor_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)

	_, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: "team-missing", Name: "actor-a"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateActor in a missing atespace = %v, want FailedPrecondition", err)
	}
	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{})
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actors after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActor_TagNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)

	_, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-a"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
		SourceTag:     &ateapipb.ObjectRef{Atespace: as, Name: "tag-missing"},
	}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("CreateActor with a missing source tag = %v, want NotFound", err)
	}
	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{Atespace: as})
	if err != nil {
		t.Fatalf("ListActors: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actors after a refused create (-want +got):\n%s", diff)
	}
}

func TestGetActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))

	got, err := h.Control.GetActor(t.Context(), &ateapipb.GetActorRequest{Actor: harness.ActorRef(actor)})
	if err != nil {
		t.Fatalf("GetActor: %v", err)
	}
	if diff := cmp.Diff(actor, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetActor (-created +got):\n%s", diff)
	}
}

func TestGetActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetActor(t.Context(), &ateapipb.GetActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetActor of a missing actor = %v, want NotFound", err)
	}
}

func TestGetActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetActor(t.Context(), &ateapipb.GetActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("GetActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestListActors(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	other := h.CreateAtespace("team-b")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	otherTmpl := h.NewTemplate(other, pool)
	const n, pageSize = 5, 2
	var created []*ateapipb.Actor
	for range n {
		created = append(created, h.NewActor(as, tmpl))
	}
	h.NewActor(other, otherTmpl)

	var listed []*ateapipb.Actor
	req := &ateapipb.ListActorsRequest{Atespace: as, PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListActors(t.Context(), req)
		if err != nil {
			t.Fatalf("ListActors page %d: %v", pages, err)
		}
		if got := len(resp.GetActors()); got > pageSize {
			t.Errorf("page %d holds %d actors, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetActors()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byName := cmpopts.SortSlices(func(a, b *ateapipb.Actor) bool { return a.GetMetadata().GetName() < b.GetMetadata().GetName() })
	if diff := cmp.Diff(created, listed, protocmp.Transform(), byName); diff != "" {
		t.Errorf("listed actors (-want +got):\n%s", diff)
	}
}

func TestListActors_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListActors with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListActors error %q does not name the invalid field page_size", msg)
	}
}

func TestListActors_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListActors with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestListActors_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	listed, err := h.Control.ListActors(t.Context(), &ateapipb.ListActorsRequest{Atespace: "team-missing"})
	if err != nil {
		t.Fatalf("ListActors in a missing atespace: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("ListActors in a missing atespace (-want +got):\n%s", diff)
	}
}

func TestUpdateActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)

	before := h.GetActor(ref)
	toUpdate := proto.Clone(before).(*ateapipb.Actor)
	toUpdate.WorkerSelector = &ateapipb.Selector{MatchLabels: map[string]string{"tier": "1"}}
	updated, err := h.Control.UpdateActor(t.Context(), &ateapipb.UpdateActorRequest{Actor: toUpdate})
	if err != nil {
		t.Fatalf("UpdateActor: %v", err)
	}

	want := proto.Clone(before).(*ateapipb.Actor)
	want.WorkerSelector = &ateapipb.Selector{MatchLabels: map[string]string{"tier": "1"}}
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated actor (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated actor has version %d, want higher than %d", got, was)
	}
	if diff := cmp.Diff(updated, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("GetActor after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)

	_, err := h.Control.UpdateActor(t.Context(), &ateapipb.UpdateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "actor-missing", Uid: foreignUID, Version: 1},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateActor of a missing actor = %v, want NotFound", err)
	}
}

func TestUpdateActor_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.Actor)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.WorkerSelector = &ateapipb.Selector{MatchLabels: map[string]string{"tier": "1"}}
		_, err := h.Control.UpdateActor(t.Context(), &ateapipb.UpdateActorRequest{Actor: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateActor with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateActor with the right uid and the wrong version = %v, want Aborted", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateActor without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)

	_, err := h.Control.UpdateActor(t.Context(), &ateapipb.UpdateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: as, Name: "Not_A_Valid_Name", Uid: foreignUID, Version: 1},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.metadata.name") {
		t.Errorf("UpdateActor error %q does not name the invalid field actor.metadata.name", msg)
	}
}

func TestDeleteActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.SuspendedActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	deleted, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: ref})
	if err != nil {
		t.Fatalf("DeleteActor: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_DELETING
	if diff := cmp.Diff(want, deleted, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("DeleteActor response (-want +got):\n%s", diff)
	}
	if _, err := h.Control.GetActor(t.Context(), &ateapipb.GetActorRequest{Actor: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetActor after delete = %v, want NotFound", err)
	}
}

func TestDeleteActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteActor of a missing actor = %v, want NotFound", err)
	}
}

func TestDeleteActor_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)
	uid, version := before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()

	_, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: ref, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActor with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActor with the right uid and the wrong version = %v, want Aborted", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteActor with the right uid and version: %v", err)
	}
}

func TestDeleteActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("DeleteActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestResumeActor(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	nodeA := h.Node("a")
	w := h.AddWorker(pool, nodeA)
	nodeB := h.Node("b")
	other := h.AddWorker(h.CreateWorkerPool("pool2", nil), nodeB)
	tmpl := h.NewTemplate(as, pool)
	golden, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: tmpl.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag()})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)

	actorBefore := h.GetActor(ref)
	workerBefore := h.GetWorker(w)
	otherBefore := h.GetWorker(other)
	objectsBefore := h.ObjectStore.Objects()
	callsBefore := len(nodeA.Atelet.Calls())
	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if err != nil {
		t.Fatalf("ResumeActor: %v", err)
	}

	if !resp.GetResumed() {
		t.Errorf("ResumeActor reported resumed = false, want true")
	}
	got := h.GetActor(ref)
	if diff := cmp.Diff(got, resp.GetActor(), protocmp.Transform()); diff != "" {
		t.Errorf("ResumeActor response against GetActor (-get +response):\n%s", diff)
	}
	wantActor := proto.Clone(actorBefore).(*ateapipb.Actor)
	wantActor.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	wantActor.Status.WorkerAssignment = &ateapipb.WorkerAssignment{
		Worker:          w.Ref(),
		WorkerNamespace: w.Namespace,
		WorkerPool:      pool.Name,
		WorkerPod:       w.Pod,
		WorkerPodUid:    w.Name,
		WorkerPodIps:    []string{w.IP},
		NodeName:        nodeA.Name,
	}
	if diff := cmp.Diff(wantActor, got, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}

	wantWorker := proto.Clone(workerBefore).(*ateapipb.Worker)
	wantWorker.Status.Allocated = &ateapipb.WorkerResources{Actors: 1}
	if diff := cmp.Diff(wantWorker, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after resume (-want +got):\n%s", diff)
	}
	assignments, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: w.Ref()})
	if err != nil {
		t.Fatalf("ListWorkerActorAssignments: %v", err)
	}
	wantAssignments := &ateapipb.ListWorkerActorAssignmentsResponse{ActorAssignments: []*ateapipb.ActorAssignment{{
		Metadata:         &ateapipb.ResourceMetadata{Name: actor.GetMetadata().GetUid()},
		Actor:            ref,
		ActorUid:         actor.GetMetadata().GetUid(),
		ActorTemplateRef: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}}}
	if diff := cmp.Diff(wantAssignments, assignments, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("worker assignments after resume (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(otherBefore, h.GetWorker(other), protocmp.Transform()); diff != "" {
		t.Errorf("unrelated worker after resume (-want +got):\n%s", diff)
	}

	calls := nodeA.Atelet.Calls()[callsBefore:]
	if len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Fatalf("node a atelet received %v, want exactly one Restore", calls)
	}
	restore := calls[0].Request.(*ateletpb.RestoreRequest)
	if got, want := restore.GetType(), ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL; got != want {
		t.Errorf("Restore type = %v, want %v", got, want)
	}
	if got, want := restore.GetExternalConfig().GetSnapshotUri(), golden.GetStatus().GetSnapshot().GetSnapshotUri(); got != want {
		t.Errorf("Restore snapshot = %q, want the golden snapshot %q", got, want)
	}
	wantSandboxes := []harness.Sandbox{{
		ActorUID:  actor.GetMetadata().GetUid(),
		Atespace:  as,
		ActorName: actor.GetMetadata().GetName(),
		WorkerUID: w.Name,
	}}
	if diff := cmp.Diff(wantSandboxes, nodeA.Atelet.Sandboxes()); diff != "" {
		t.Errorf("node a sandboxes after resume (-want +got):\n%s", diff)
	}
	if calls := nodeB.Atelet.Calls(); len(calls) != 0 {
		t.Errorf("node b atelet received %v, want no calls", calls)
	}
	if diff := cmp.Diff(objectsBefore, h.ObjectStore.Objects()); diff != "" {
		t.Errorf("object storage after resume (-want +got):\n%s", diff)
	}
}

func TestResumeActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ResumeActor of a missing actor = %v, want NotFound", err)
	}
}

func TestResumeActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ResumeActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("ResumeActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestResumeActor_RestoreFails(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	tmpl := h.NewTemplate(as, pool)
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	node.Atelet.On(harness.AteletRestore).FailNext(status.Error(codes.Internal, "injected restore failure"))

	actorBefore := h.GetActor(ref)
	workerBefore := h.GetWorker(w)
	callsBefore := len(node.Atelet.Calls())
	_, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Internal {
		t.Fatalf("ResumeActor with a failing Restore = %v, want Internal", err)
	}

	wantActor := proto.Clone(actorBefore).(*ateapipb.Actor)
	wantActor.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	wantActor.Status.Crash = &ateapipb.ActorCrash{Message: "resume failed: atelet Restore: injected restore failure"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	crashed := h.GetActor(ref)
	if diff := cmp.Diff(wantActor, crashed, protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after a failed Restore (-want +got):\n%s", diff)
	}
	if crashed.GetStatus().GetCrash().GetCrashTime() == nil {
		t.Errorf("status.crash of the crashed actor has no crash time")
	}
	if diff := cmp.Diff(workerBefore, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after a failed Restore (-want +got):\n%s", diff)
	}
	assignments, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: w.Ref()})
	if err != nil {
		t.Fatalf("ListWorkerActorAssignments: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListWorkerActorAssignmentsResponse{}, assignments, protocmp.Transform()); diff != "" {
		t.Errorf("worker assignments after a failed Restore (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Errorf("atelet received %v, want exactly one Restore", calls)
	}
	if diff := cmp.Diff([]harness.Sandbox{}, node.Atelet.Sandboxes()); diff != "" {
		t.Errorf("sandboxes after a failed Restore (-want +got):\n%s", diff)
	}
}

func TestResumeActor_RestoreUnavailable(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	tmpl := h.NewTemplate(as, pool)
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	node.Atelet.On(harness.AteletRestore).FailNext(harness.ErrUnavailable)

	actorBefore := h.GetActor(ref)
	workerBefore := h.GetWorker(w)
	callsBefore := len(node.Atelet.Calls())
	_, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("ResumeActor with an unavailable atelet = %v, want Unavailable", err)
	}

	wantActor := proto.Clone(actorBefore).(*ateapipb.Actor)
	wantActor.Status.State = ateapipb.ActorState_ACTOR_STATE_RESUMING
	wantActor.Status.WorkerAssignment = &ateapipb.WorkerAssignment{
		Worker:          w.Ref(),
		WorkerNamespace: w.Namespace,
		WorkerPool:      pool.Name,
		WorkerPod:       w.Pod,
		WorkerPodUid:    w.Name,
		WorkerPodIps:    []string{w.IP},
		NodeName:        node.Name,
	}
	if diff := cmp.Diff(wantActor, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("actor after an unavailable atelet (-want +got):\n%s", diff)
	}
	wantWorker := proto.Clone(workerBefore).(*ateapipb.Worker)
	wantWorker.Status.Allocated = &ateapipb.WorkerResources{Actors: 1}
	if diff := cmp.Diff(wantWorker, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after an unavailable atelet (-want +got):\n%s", diff)
	}
	assignments, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: w.Ref()})
	if err != nil {
		t.Fatalf("ListWorkerActorAssignments: %v", err)
	}
	wantAssignments := &ateapipb.ListWorkerActorAssignmentsResponse{ActorAssignments: []*ateapipb.ActorAssignment{{
		Metadata:         &ateapipb.ResourceMetadata{Name: actor.GetMetadata().GetUid()},
		Actor:            ref,
		ActorUid:         actor.GetMetadata().GetUid(),
		ActorTemplateRef: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}}}
	if diff := cmp.Diff(wantAssignments, assignments, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("worker assignments after an unavailable atelet (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Errorf("atelet received %v, want exactly one Restore", calls)
	}
	if diff := cmp.Diff([]harness.Sandbox{}, node.Atelet.Sandboxes()); diff != "" {
		t.Errorf("sandboxes after an unavailable atelet (-want +got):\n%s", diff)
	}
}

func TestResumeActor_WrongState(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	crashed := h.CrashedActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(crashed)
	before := h.GetActor(ref)

	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ResumeActor of a crashed actor = %v, want FailedPrecondition", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused resume (-want +got):\n%s", diff)
	}
}

func TestResumeActor_AlreadyRunning(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	h.AddWorker(pool, node)
	running := h.RunningActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(running)
	before := h.GetActor(ref)
	callsBefore := len(node.Atelet.Calls())

	resp, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor of a running actor = %v, want OK", err)
	}
	if resp.GetResumed() {
		t.Error("ResumeActor of a running actor reported resumed = true, want false")
	}
	if diff := cmp.Diff(before, resp.GetActor(), protocmp.Transform()); diff != "" {
		t.Errorf("ResumeActor response (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after resuming it while running (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 0 {
		t.Errorf("atelet received %v, want no calls", calls)
	}
}

func TestResumeActor_FromSuspended(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	suspended := h.SuspendedActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(suspended)
	before := h.GetActor(ref)
	callsBefore := len(node.Atelet.Calls())

	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor of a suspended actor = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(w)
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
	calls := node.Atelet.Calls()[callsBefore:]
	if len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Fatalf("atelet received %v, want exactly one Restore", calls)
	}
	restore := calls[0].Request.(*ateletpb.RestoreRequest)
	if got, want := restore.GetType(), ateletpb.CheckpointType_CHECKPOINT_TYPE_EXTERNAL; got != want {
		t.Errorf("Restore type = %v, want %v", got, want)
	}
	if got, want := restore.GetExternalConfig().GetSnapshotUri(), before.GetStatus().GetExternalSnapshot().GetSnapshotUri(); got != want {
		t.Errorf("Restore snapshot = %q, want the actor's own snapshot %q", got, want)
	}
}

func TestResumeActor_FromPaused(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	paused := h.PausedActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(paused)
	before := h.GetActor(ref)
	callsBefore := len(node.Atelet.Calls())

	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor of a paused actor = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(w)
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
	calls := node.Atelet.Calls()[callsBefore:]
	if len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Fatalf("atelet received %v, want exactly one Restore", calls)
	}
	restore := calls[0].Request.(*ateletpb.RestoreRequest)
	if got, want := restore.GetType(), ateletpb.CheckpointType_CHECKPOINT_TYPE_LOCAL; got != want {
		t.Errorf("Restore type = %v, want %v", got, want)
	}
	if got, want := restore.GetLocalConfig().GetSnapshotName(), before.GetStatus().GetLocalSnapshot().GetSnapshotName(); got != want {
		t.Errorf("Restore local snapshot = %q, want the actor's %q", got, want)
	}
}

func TestResumeActor_WorkerFull(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	tmpl := h.NewTemplate(as, pool)
	other := h.RunningActor(as, tmpl)
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)

	before := h.GetActor(ref)
	callsBefore := len(node.Atelet.Calls())
	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ResumeActor with the only worker full = %v, want ResourceExhausted", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused resume (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 0 {
		t.Errorf("atelet received %v after a refused resume, want no calls", calls)
	}

	if _, err := h.Control.SuspendActor(t.Context(), &ateapipb.SuspendActorRequest{Actor: harness.ActorRef(other)}); err != nil {
		t.Fatalf("SuspendActor: %v", err)
	}
	before = h.GetActor(ref)
	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor after freeing the worker = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(w)
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
}

func TestResumeActor_WorkerDraining(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	tmpl := h.NewTemplate(as, pool)
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	w.Drain()

	before := h.GetActor(ref)
	drainingBefore := h.GetWorker(w)
	callsBefore := len(node.Atelet.Calls())
	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ResumeActor with the only worker draining = %v, want ResourceExhausted", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused resume (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(drainingBefore, h.GetWorker(w), protocmp.Transform()); diff != "" {
		t.Errorf("draining worker after a refused resume (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 0 {
		t.Errorf("atelet received %v after a refused resume, want no calls", calls)
	}

	fresh := h.AddWorker(pool, node)
	before = h.GetActor(ref)
	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor after adding a worker = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(fresh)
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
}

func TestResumeActor_WorkerRemoved(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	tmpl := h.NewTemplate(as, pool)
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	w.Remove()

	before := h.GetActor(ref)
	callsBefore := len(node.Atelet.Calls())
	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("ResumeActor with the only worker removed = %v, want ResourceExhausted", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused resume (-want +got):\n%s", diff)
	}
	if calls := node.Atelet.Calls()[callsBefore:]; len(calls) != 0 {
		t.Errorf("atelet received %v after a refused resume, want no calls", calls)
	}

	fresh := h.AddWorker(pool, node)
	before = h.GetActor(ref)
	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor after adding a worker = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(fresh)
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
}

func TestResumeActor_Retry(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	w := h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	before := resumingActor(t, h, as, tmpl, w)
	ref := harness.ActorRef(before)
	workerBefore := h.GetWorker(w)

	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor retry = %v, want OK", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(workerBefore, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after the retry (-want +got):\n%s", diff)
	}
	assignments, err := h.Control.ListWorkerActorAssignments(t.Context(), &ateapipb.ListWorkerActorAssignmentsRequest{Worker: w.Ref()})
	if err != nil {
		t.Fatalf("ListWorkerActorAssignments: %v", err)
	}
	wantAssignments := &ateapipb.ListWorkerActorAssignmentsResponse{ActorAssignments: []*ateapipb.ActorAssignment{{
		Metadata:         &ateapipb.ResourceMetadata{Name: before.GetMetadata().GetUid()},
		Actor:            ref,
		ActorUid:         before.GetMetadata().GetUid(),
		ActorTemplateRef: &ateapipb.ObjectRef{Atespace: as, Name: tmpl.GetMetadata().GetName()},
	}}}
	if diff := cmp.Diff(wantAssignments, assignments, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("worker assignments after the retry (-want +got):\n%s", diff)
	}
}

func TestResumeActor_RetryWorkerDrained(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	w := h.AddWorker(pool, h.Node("a"))
	before := resumingActor(t, h, as, h.NewTemplate(as, pool), w)
	ref := harness.ActorRef(before)
	w.Drain()

	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Aborted {
		t.Errorf("ResumeActor retry on a draining worker = %v, want Aborted", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	want.Status.WorkerAssignment = nil
	want.Status.Crash = &ateapipb.ActorCrash{Message: "resume failed: assigned worker is draining"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after a retry on a draining worker (-want +got):\n%s", diff)
	}
}

func TestResumeActor_RetryWorkerRemoved(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	w := h.AddWorker(pool, h.Node("a"))
	before := resumingActor(t, h, as, h.NewTemplate(as, pool), w)
	ref := harness.ActorRef(before)
	w.Remove()

	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	want.Status.WorkerAssignment = nil
	want.Status.Crash = &ateapipb.ActorCrash{Message: "resume failed: worker pod went away while hosting the actor"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after its worker was removed (-want +got):\n%s", diff)
	}
	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ResumeActor of the crashed actor = %v, want FailedPrecondition", err)
	}
}

func TestResumeActor_RetryWorkerIneligible(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	w := h.AddWorker(pool, h.Node("a"))
	before := resumingActor(t, h, as, h.NewTemplate(as, pool), w)
	ref := harness.ActorRef(before)
	relabeled := proto.Clone(h.GetWorker(w)).(*ateapipb.Worker)
	relabeled.Labels = map[string]string{"pool": "other"}
	relabeled, err := h.Control.UpdateWorker(t.Context(), &ateapipb.UpdateWorkerRequest{Worker: relabeled})
	if err != nil {
		t.Fatalf("UpdateWorker: %v", err)
	}

	_, err = h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Aborted {
		t.Errorf("ResumeActor retry on an ineligible worker = %v, want Aborted", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	want.Status.WorkerAssignment = nil
	want.Status.Crash = &ateapipb.ActorCrash{Message: "resume failed: assigned worker no longer satisfies the actor's placement constraints"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after a retry on an ineligible worker (-want +got):\n%s", diff)
	}
	wantWorker := proto.Clone(relabeled).(*ateapipb.Worker)
	wantWorker.Status.Allocated = nil
	if diff := cmp.Diff(wantWorker, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after a retry on it while ineligible (-want +got):\n%s", diff)
	}
}

func TestResumeActor_WithVolume(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	actor := h.NewActor(as, h.NewTemplate(as, pool, harness.WithExternalVolume("data", "/data")))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor = %v, want OK", err)
	}
	volumeName := "substrate-" + actor.GetMetadata().GetUid() + "-data"
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	want.Status.WorkerAssignment = workerAssignment(w)
	want.Status.ActorVolumes = []*ateapipb.ExternalVolume{{
		VolumeName:      "data",
		StorageVolumeId: "vol-" + volumeName,
		VolumeType:      harness.VolumeDriver,
		Status:          ateapipb.ExternalVolume_STATUS_CREATED,
	}}
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("resumed actor (-want +got):\n%s", diff)
	}
	wantVolumes := []harness.Volume{{ID: "vol-" + volumeName, Name: volumeName, AttachedTo: []string{node.Name}}}
	if diff := cmp.Diff(wantVolumes, h.Volumes.List()); diff != "" {
		t.Errorf("volumes after resume (-want +got):\n%s", diff)
	}
}

func TestResumeActor_VolumeCreateFails(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	h.AddWorker(pool, node)
	actor := h.NewActor(as, h.NewTemplate(as, pool, harness.WithExternalVolume("data", "/data")))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)
	h.Volumes.On(harness.VolumeCreate).FailNext(harness.ErrUnavailable)

	_, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Internal {
		t.Fatalf("ResumeActor with a failing CreateVolume = %v, want Internal", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("actor after a failed CreateVolume (-want +got):\n%s", diff)
	}

	if _, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref}); status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor retry = %v, want OK", err)
	}
	volumeName := "substrate-" + actor.GetMetadata().GetUid() + "-data"
	wantVolumes := []harness.Volume{{ID: "vol-" + volumeName, Name: volumeName, AttachedTo: []string{node.Name}}}
	if diff := cmp.Diff(wantVolumes, h.Volumes.List()); diff != "" {
		t.Errorf("volumes after the retry (-want +got):\n%s", diff)
	}
}

func TestResumeActor_VolumeAttachFails(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	actor := h.NewActor(as, h.NewTemplate(as, pool, harness.WithExternalVolume("data", "/data")))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)
	h.Volumes.On(harness.VolumeAttach).FailNext(harness.ErrUnavailable)

	_, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Internal {
		t.Fatalf("ResumeActor with a failing AttachVolume = %v, want Internal", err)
	}
	volumeName := "substrate-" + actor.GetMetadata().GetUid() + "-data"
	createdVolume := []*ateapipb.ExternalVolume{{
		VolumeName:      "data",
		StorageVolumeId: "vol-" + volumeName,
		VolumeType:      harness.VolumeDriver,
		Status:          ateapipb.ExternalVolume_STATUS_CREATED,
	}}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RESUMING
	want.Status.WorkerAssignment = workerAssignment(w)
	want.Status.ActorVolumes = createdVolume
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("actor after a failed AttachVolume (-want +got):\n%s", diff)
	}

	resp, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor retry = %v, want OK", err)
	}
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	if diff := cmp.Diff(want, resp.GetActor(), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("actor after the retry (-want +got):\n%s", diff)
	}
	wantVolumes := []harness.Volume{{ID: "vol-" + volumeName, Name: volumeName, AttachedTo: []string{node.Name}}}
	if diff := cmp.Diff(wantVolumes, h.Volumes.List()); diff != "" {
		t.Errorf("volumes after the retry (-want +got):\n%s", diff)
	}
}

func TestResumeActor_WithResourceLimits(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node, harness.WithResourceCapacity("2", "4Gi"))
	limits := []*ateapipb.Limits{{Name: "cpu", Quantity: "500m"}, {Name: "memory", Quantity: "1Gi"}}
	tmpl := h.NewTemplate(as, pool, func(tmpl *ateapipb.ActorTemplate) {
		tmpl.Resources = &ateapipb.Resources{Limits: limits}
	})
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	workerBefore := h.GetWorker(w)
	callsBefore := len(node.Atelet.Calls())

	if _, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref}); status.Code(err) != codes.OK {
		t.Fatalf("ResumeActor = %v, want OK", err)
	}
	calls := node.Atelet.Calls()[callsBefore:]
	if len(calls) != 1 || calls[0].Op != harness.AteletRestore {
		t.Fatalf("atelet received %v, want exactly one Restore", calls)
	}
	restore := calls[0].Request.(*ateletpb.RestoreRequest)
	if got, want := restore.GetCpuMilli(), int64(500); got != want {
		t.Errorf("Restore cpu_milli = %d, want %d", got, want)
	}
	if got, want := restore.GetMemoryBytes(), int64(1<<30); got != want {
		t.Errorf("Restore memory_bytes = %d, want %d", got, want)
	}
	wantWorker := proto.Clone(workerBefore).(*ateapipb.Worker)
	wantWorker.Status.Allocated = &ateapipb.WorkerResources{Actors: 1, Resources: &ateapipb.Resources{Limits: limits}}
	if diff := cmp.Diff(wantWorker, h.GetWorker(w), protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("worker after resume (-want +got):\n%s", diff)
	}
}

func TestResumeActor_Concurrent(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	node := h.Node("a")
	w := h.AddWorker(pool, node)
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)

	gate := node.Atelet.On(harness.AteletRestore).Block()
	first := make(chan error, 1)
	go func() {
		_, err := h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
		first <- err
	}()
	gate.Wait(t)
	_, err := h.Control.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Aborted {
		t.Errorf("second ResumeActor while the first is held = %v, want Aborted", err)
	}
	gate.Release()
	if err := <-first; status.Code(err) != codes.OK {
		t.Fatalf("first ResumeActor = %v, want OK", err)
	}
	wantSandboxes := []harness.Sandbox{{
		ActorUID:  actor.GetMetadata().GetUid(),
		Atespace:  as,
		ActorName: actor.GetMetadata().GetName(),
		WorkerUID: w.Name,
	}}
	if diff := cmp.Diff(wantSandboxes, node.Atelet.Sandboxes()); diff != "" {
		t.Errorf("sandboxes after both resumes (-want +got):\n%s", diff)
	}
}

func TestResumeActor_GoldenSnapshotMissing(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	golden, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: tmpl.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag()})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	goldenURI := golden.GetStatus().GetSnapshot().GetSnapshotUri()
	if err := h.ObjectStore.DeleteSnapshot(goldenURI); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	actor := h.NewActor(as, tmpl)
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	_, err = h.ResumeActor(t.Context(), &ateapipb.ResumeActorRequest{Actor: ref})
	if status.Code(err) != codes.Internal {
		t.Fatalf("ResumeActor with the golden snapshot missing = %v, want Internal", err)
	}
	want := proto.Clone(before).(*ateapipb.Actor)
	want.Status.State = ateapipb.ActorState_ACTOR_STATE_CRASHED
	want.Status.Crash = &ateapipb.ActorCrash{Message: "resume failed: atelet Restore: while fetching the snapshot manifest: " + goldenURI + " not found"}
	ignoreCrashTime := protocmp.IgnoreFields(&ateapipb.ActorCrash{}, "crash_time")
	if diff := cmp.Diff(want, h.GetActor(ref), protocmp.Transform(), ignoreVersion, ignoreTimestamps, ignoreCrashTime); diff != "" {
		t.Errorf("actor after a missing golden snapshot (-want +got):\n%s", diff)
	}
}

func TestSuspendActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.SuspendActor(t.Context(), &ateapipb.SuspendActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("SuspendActor of a missing actor = %v, want NotFound", err)
	}
}

func TestSuspendActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.SuspendActor(t.Context(), &ateapipb.SuspendActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("SuspendActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("SuspendActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestSuspendActor_WrongState(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.CrashedActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	_, err := h.Control.SuspendActor(t.Context(), &ateapipb.SuspendActorRequest{Actor: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("SuspendActor of a crashed actor = %v, want FailedPrecondition", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused SuspendActor (-want +got):\n%s", diff)
	}
}

func TestPauseActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.PauseActor(t.Context(), &ateapipb.PauseActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("PauseActor of a missing actor = %v, want NotFound", err)
	}
}

func TestPauseActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.PauseActor(t.Context(), &ateapipb.PauseActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("PauseActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("PauseActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestPauseActor_WrongState(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	_, err := h.Control.PauseActor(t.Context(), &ateapipb.PauseActorRequest{Actor: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("PauseActor of a suspended actor = %v, want FailedPrecondition", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused PauseActor (-want +got):\n%s", diff)
	}
}

func TestRevertActor_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.RevertActor(t.Context(), &ateapipb.RevertActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("RevertActor of a missing actor = %v, want NotFound", err)
	}
}

func TestRevertActor_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.RevertActor(t.Context(), &ateapipb.RevertActorRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RevertActor with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("RevertActor error %q does not name the invalid field actor.name", msg)
	}
}

func TestRevertActor_WrongState(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	actor := h.NewActor(as, h.NewTemplate(as, pool))
	ref := harness.ActorRef(actor)
	before := h.GetActor(ref)

	_, err := h.Control.RevertActor(t.Context(), &ateapipb.RevertActorRequest{Actor: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RevertActor of a suspended actor = %v, want FailedPrecondition", err)
	}
	if diff := cmp.Diff(before, h.GetActor(ref), protocmp.Transform()); diff != "" {
		t.Errorf("actor after a refused RevertActor (-want +got):\n%s", diff)
	}
}

func TestMintActorJWT_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.MintActorJWT(t.Context(), &ateapipb.MintActorJWTRequest{
		Actor:             &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"},
		Audience:          []string{"https://audience.example"},
		ExpirationSeconds: 600,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("MintActorJWT for a missing actor = %v, want NotFound", err)
	}
}

func TestMintActorJWT_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.MintActorJWT(t.Context(), &ateapipb.MintActorJWTRequest{
		Actor:             &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		Audience:          []string{"https://audience.example"},
		ExpirationSeconds: 600,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("MintActorJWT with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("MintActorJWT error %q does not name the invalid field actor.name", msg)
	}
}

func TestMintActorCertificate_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.MintActorCertificate(t.Context(), &ateapipb.MintActorCertificateRequest{
		Actor:                     &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		ActorUid:                  foreignUID,
		CertificateSigningRequest: certificateSigningRequest(t),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("MintActorCertificate with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("MintActorCertificate error %q does not name the invalid field actor.name", msg)
	}
}

func TestMintAteomActorCertificate_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	node := h.Node("a")

	_, err := node.WorkerService().MintAteomActorCertificate(t.Context(), &ateapipb.MintAteomActorCertificateRequest{
		Actor:                     &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"},
		ActorUid:                  foreignUID,
		CertificateSigningRequest: certificateSigningRequest(t),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("MintAteomActorCertificate for a missing actor = %v, want NotFound", err)
	}
}

func TestMintAteomActorCertificate_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	node := h.Node("a")

	_, err := node.WorkerService().MintAteomActorCertificate(t.Context(), &ateapipb.MintAteomActorCertificateRequest{
		Actor:                     &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		ActorUid:                  foreignUID,
		CertificateSigningRequest: certificateSigningRequest(t),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("MintAteomActorCertificate with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("MintAteomActorCertificate error %q does not name the invalid field actor.name", msg)
	}
}

func TestRequestActorSuspend_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	node := h.Node("a")
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), node)

	_, err := node.WorkerService().RequestActorSuspend(t.Context(), &ateapipb.RequestActorSuspendRequest{
		Worker:   w.Ref(),
		Actor:    &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"},
		ActorUid: foreignUID,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("RequestActorSuspend of a missing actor = %v, want NotFound", err)
	}
}

func TestRequestActorSuspend_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	node := h.Node("a")
	w := h.AddWorker(h.CreateWorkerPool("pool1", nil), node)

	_, err := node.WorkerService().RequestActorSuspend(t.Context(), &ateapipb.RequestActorSuspendRequest{
		Worker:   w.Ref(),
		Actor:    &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		ActorUid: foreignUID,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RequestActorSuspend with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("RequestActorSuspend error %q does not name the invalid field actor.name", msg)
	}
}
