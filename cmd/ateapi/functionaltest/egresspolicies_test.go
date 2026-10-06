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
	"strings"
	"testing"

	"github.com/agent-substrate/substrate/cmd/ateapi/functionaltest/harness"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// egressPolicySpec is a valid egress policy for an actor in atespace.
func egressPolicySpec(atespace string) *ateapipb.EgressPolicy {
	return &ateapipb.EgressPolicy{
		Metadata: &ateapipb.ResourceMetadata{Atespace: atespace, Name: "default"},
		Rules:    []*ateapipb.EgressRule{{Http: &ateapipb.HTTPRule{Hostnames: []string{"api.example.com"}}}},
	}
}

// egressPolicyActor creates an actor for an egress policy to belong to.
func egressPolicyActor(h *harness.Harness, as string) *ateapipb.ObjectRef {
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	return harness.ActorRef(h.NewActor(as, h.NewTemplate(as, pool)))
}

func TestCreateActorEgressPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)

	created, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}
	want := &ateapipb.EgressPolicy{
		Metadata: &ateapipb.ResourceMetadata{Atespace: as, Name: "default"},
		Rules: []*ateapipb.EgressRule{{Http: &ateapipb.HTTPRule{
			Hostnames: []string{"api.example.com"},
			Ports:     &ateapipb.Ports{Numbers: []int32{80}},
		}}},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created egress policy (-want +got):\n%s", diff)
	}
}

func TestCreateActorEgressPolicy_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	created, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}

	_, err = h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateActorEgressPolicy for an actor that has one = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("GetActorEgressPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("egress policy after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActorEgressPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{
		Actor:        &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		EgressPolicy: egressPolicySpec(as),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateActorEgressPolicy with an invalid actor name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("CreateActorEgressPolicy error %q does not name the invalid field actor.name", msg)
	}
}

func TestCreateActorEgressPolicy_ActorNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{
		Actor:        &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"},
		EgressPolicy: egressPolicySpec(as),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateActorEgressPolicy for a missing actor = %v, want FailedPrecondition", err)
	}
}

func TestGetActorEgressPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	created, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}

	got, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("GetActorEgressPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetActorEgressPolicy (-created +got):\n%s", diff)
	}
}

func TestGetActorEgressPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)

	_, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetActorEgressPolicy for an actor with none = %v, want NotFound", err)
	}
}

func TestGetActorEgressPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetActorEgressPolicy with an invalid actor name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("GetActorEgressPolicy error %q does not name the invalid field actor.name", msg)
	}
}

func TestGetActorEgressPolicy_ActorDeleted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	if _, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)}); err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}
	template := h.GetActor(actor).GetActorTemplate()

	if _, err := h.Control.DeleteActor(t.Context(), &ateapipb.DeleteActorRequest{Actor: actor}); err != nil {
		t.Fatalf("DeleteActor: %v", err)
	}
	_, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetActorEgressPolicy of a deleted actor = %v, want NotFound", err)
	}

	if _, err := h.Control.CreateActor(t.Context(), &ateapipb.CreateActorRequest{Actor: &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: actor.GetAtespace(), Name: actor.GetName()},
		ActorTemplate: template,
	}}); err != nil {
		t.Fatalf("CreateActor under the same name: %v", err)
	}
	_, err = h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetActorEgressPolicy of an actor recreated under the same name = %v, want NotFound", err)
	}
}

func TestUpdateActorEgressPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	before, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}
	rules := []*ateapipb.EgressRule{{TlsPassthrough: &ateapipb.TLSPassthroughRule{Hostnames: []string{"*"}, Ports: &ateapipb.Ports{Numbers: []int32{443}}}}}

	toUpdate := proto.Clone(before).(*ateapipb.EgressPolicy)
	toUpdate.Rules = rules
	updated, err := h.Control.UpdateActorEgressPolicy(t.Context(), &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: toUpdate})
	if err != nil {
		t.Fatalf("UpdateActorEgressPolicy: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.EgressPolicy)
	want.Rules = rules
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated egress policy (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated egress policy has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("GetActorEgressPolicy: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetActorEgressPolicy after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateActorEgressPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	policy := egressPolicySpec(as)
	policy.Metadata.Uid, policy.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateActorEgressPolicy(t.Context(), &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: policy})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateActorEgressPolicy for an actor with none = %v, want NotFound", err)
	}
}

func TestUpdateActorEgressPolicy_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	before, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.EgressPolicy)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.Rules = []*ateapipb.EgressRule{{Http: &ateapipb.HTTPRule{Hostnames: []string{"other.example.com"}}}}
		_, err := h.Control.UpdateActorEgressPolicy(t.Context(), &ateapipb.UpdateActorEgressPolicyRequest{Actor: actor, EgressPolicy: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateActorEgressPolicy with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateActorEgressPolicy with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("GetActorEgressPolicy: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("egress policy after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateActorEgressPolicy without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateActorEgressPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	policy := egressPolicySpec(as)
	policy.Metadata.Uid, policy.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateActorEgressPolicy(t.Context(), &ateapipb.UpdateActorEgressPolicyRequest{
		Actor:        &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"},
		EgressPolicy: policy,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateActorEgressPolicy with an invalid actor name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("UpdateActorEgressPolicy error %q does not name the invalid field actor.name", msg)
	}
}

func TestDeleteActorEgressPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	created, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}

	deleted, err := h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("DeleteActorEgressPolicy: %v", err)
	}
	if diff := cmp.Diff(created, deleted, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteActorEgressPolicy response (-created +deleted):\n%s", diff)
	}
	if _, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor}); status.Code(err) != codes.NotFound {
		t.Errorf("GetActorEgressPolicy after delete = %v, want NotFound", err)
	}
}

func TestDeleteActorEgressPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)

	_, err := h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: actor})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteActorEgressPolicy for an actor with none = %v, want NotFound", err)
	}
}

func TestDeleteActorEgressPolicy_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	actor := egressPolicyActor(h, as)
	created, err := h.Control.CreateActorEgressPolicy(t.Context(), &ateapipb.CreateActorEgressPolicyRequest{Actor: actor, EgressPolicy: egressPolicySpec(as)})
	if err != nil {
		t.Fatalf("CreateActorEgressPolicy: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()

	_, err = h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: actor, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActorEgressPolicy with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: actor, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActorEgressPolicy with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetActorEgressPolicy(t.Context(), &ateapipb.GetActorEgressPolicyRequest{Actor: actor})
	if err != nil {
		t.Fatalf("GetActorEgressPolicy after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("egress policy after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: actor, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteActorEgressPolicy with the right uid and version: %v", err)
	}
}

func TestDeleteActorEgressPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteActorEgressPolicy(t.Context(), &ateapipb.DeleteActorEgressPolicyRequest{Actor: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteActorEgressPolicy with an invalid actor name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor.name") {
		t.Errorf("DeleteActorEgressPolicy error %q does not name the invalid field actor.name", msg)
	}
}
