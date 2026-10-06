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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// accessPolicySpec is a valid access policy for an atespace or the global
// scope: both accept the viewer role.
func accessPolicySpec() *ateapipb.AccessPolicy {
	return &ateapipb.AccessPolicy{
		Metadata: &ateapipb.ResourceMetadata{Name: "default"},
		Bindings: []*ateapipb.Binding{{Role: "viewer", Members: []string{harness.Member("alice")}}},
	}
}

// setAtespaceBinding makes the atespace's access policy, as the admin, bind
// exactly member to role, creating the policy if the atespace has none.
func setAtespaceBinding(t *testing.T, h *harness.Harness, atespace *ateapipb.ObjectRef, role, member string) *ateapipb.AccessPolicy {
	t.Helper()
	bindings := []*ateapipb.Binding{{Role: role, Members: []string{member}}}
	current, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: atespace})
	if status.Code(err) == codes.NotFound {
		created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: atespace, AccessPolicy: &ateapipb.AccessPolicy{
			Metadata: &ateapipb.ResourceMetadata{Name: "default"},
			Bindings: bindings,
		}})
		if err != nil {
			t.Fatalf("binding %s to %s: %v", member, role, err)
		}
		return created
	}
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy: %v", err)
	}
	toUpdate := proto.Clone(current).(*ateapipb.AccessPolicy)
	toUpdate.Bindings = bindings
	updated, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: atespace, AccessPolicy: toUpdate})
	if err != nil {
		t.Fatalf("binding %s to %s: %v", member, role, err)
	}
	return updated
}

// eventuallyAllowed calls call until it no longer fails with
// PermissionDenied, as a grant may take a while to apply, and fails the test
// unless that first allowed call's result has the code want. It stops at the
// first allowed call, so call may change state.
func eventuallyAllowed(t *testing.T, h *harness.Harness, what string, want codes.Code, call func() error) {
	t.Helper()
	var err error
	h.Eventually(what+" to be allowed", func() bool {
		err = call()
		return status.Code(err) != codes.PermissionDenied
	})
	if status.Code(err) != want {
		t.Errorf("%s = %v, want %v", what, err, want)
	}
}

// setGlobalBinding makes the global access policy, as the admin, bind exactly
// member to role, creating the policy if it does not exist.
func setGlobalBinding(t *testing.T, h *harness.Harness, role, member string) *ateapipb.AccessPolicy {
	t.Helper()
	bindings := []*ateapipb.Binding{{Role: role, Members: []string{member}}}
	current, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if status.Code(err) == codes.NotFound {
		created, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: &ateapipb.AccessPolicy{
			Metadata: &ateapipb.ResourceMetadata{Name: "default"},
			Bindings: bindings,
		}})
		if err != nil {
			t.Fatalf("binding %s to %s: %v", member, role, err)
		}
		return created
	}
	if err != nil {
		t.Fatalf("GetGlobalAccessPolicy: %v", err)
	}
	toUpdate := proto.Clone(current).(*ateapipb.AccessPolicy)
	toUpdate.Bindings = bindings
	updated, err := h.Control.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: toUpdate})
	if err != nil {
		t.Fatalf("binding %s to %s: %v", member, role, err)
	}
	return updated
}

func TestCreateAtespaceAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}

	created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}
	want := &ateapipb.AccessPolicy{
		Metadata: &ateapipb.ResourceMetadata{Name: "default"},
		Bindings: []*ateapipb.Binding{{Role: "viewer", Members: []string{harness.Member("alice")}}},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created access policy (-want +got):\n%s", diff)
	}
}

func TestCreateAtespaceAccessPolicy_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}

	_, err = h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateAtespaceAccessPolicy for an atespace that has one = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("access policy after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateAtespaceAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{
		Atespace:     &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"},
		AccessPolicy: accessPolicySpec(),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateAtespaceAccessPolicy with an invalid atespace name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("CreateAtespaceAccessPolicy error %q does not name the invalid field atespace.name", msg)
	}
}

func TestCreateAtespaceAccessPolicy_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{
		Atespace:     &ateapipb.ObjectRef{Name: "atespace-missing"},
		AccessPolicy: accessPolicySpec(),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateAtespaceAccessPolicy for a missing atespace = %v, want FailedPrecondition", err)
	}
}

func TestCreateAtespaceAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	create := func() error {
		_, err := alice.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
		return err
	}

	if err := create(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("CreateAtespaceAccessPolicy holding no role = %v, want PermissionDenied", err)
	}
	if _, err := alice.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}, AccessPolicy: accessPolicySpec()}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("CreateAtespaceAccessPolicy for a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	if _, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as}); status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy after a refused create = %v, want NotFound", err)
	}
	for _, role := range []string{"viewer", "editor"} {
		setAtespaceBinding(t, h, as, role, harness.Member("alice"))
		if err := create(); status.Code(err) != codes.PermissionDenied {
			t.Errorf("CreateAtespaceAccessPolicy holding %s = %v, want PermissionDenied", role, err)
		}
	}
	setAtespaceBinding(t, h, as, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "CreateAtespaceAccessPolicy holding owner", codes.AlreadyExists, create)
}

func TestGetAtespaceAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}

	got, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetAtespaceAccessPolicy (-created +got):\n%s", diff)
	}
}

func TestGetAtespaceAccessPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}

	_, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy for an atespace with none = %v, want NotFound", err)
	}
}

func TestGetAtespaceAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetAtespaceAccessPolicy with an invalid atespace name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("GetAtespaceAccessPolicy error %q does not name the invalid field atespace.name", msg)
	}
}

func TestGetAtespaceAccessPolicy_AtespaceDeleted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	if _, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()}); err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}

	if _, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: as}); err != nil {
		t.Fatalf("DeleteAtespace: %v", err)
	}
	_, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy of a deleted atespace = %v, want NotFound", err)
	}

	h.CreateAtespace(as.GetName())
	_, err = h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy of an atespace recreated under the same name = %v, want NotFound", err)
	}
}

func TestGetAtespaceAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
		return err
	}

	setAtespaceBinding(t, h, as, "viewer", harness.Member("bob"))
	if err := get(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetAtespaceAccessPolicy holding no role = %v, want PermissionDenied", err)
	}
	if _, err := alice.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetAtespaceAccessPolicy of a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	for _, role := range []string{"viewer", "editor", "owner"} {
		setAtespaceBinding(t, h, as, role, harness.Member("alice"))
		eventuallyAllowed(t, h, fmt.Sprintf("GetAtespaceAccessPolicy holding %s", role), codes.OK, get)
	}
}

func TestUpdateAtespaceAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	before, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}
	bindings := []*ateapipb.Binding{{Role: "editor", Members: []string{harness.Member("bob")}}}

	toUpdate := proto.Clone(before).(*ateapipb.AccessPolicy)
	toUpdate.Bindings = bindings
	updated, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: toUpdate})
	if err != nil {
		t.Fatalf("UpdateAtespaceAccessPolicy: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.AccessPolicy)
	want.Bindings = bindings
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated access policy (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated access policy has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetAtespaceAccessPolicy after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateAtespaceAccessPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	policy := accessPolicySpec()
	policy.Metadata.Uid, policy.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: policy})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateAtespaceAccessPolicy for an atespace with none = %v, want NotFound", err)
	}
}

func TestUpdateAtespaceAccessPolicy_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	before, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.AccessPolicy)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.Bindings = []*ateapipb.Binding{{Role: "editor", Members: []string{harness.Member("bob")}}}
		_, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateAtespaceAccessPolicy with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateAtespaceAccessPolicy with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("access policy after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateAtespaceAccessPolicy without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateAtespaceAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	policy := accessPolicySpec()
	policy.Metadata.Uid, policy.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{
		Atespace:     &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"},
		AccessPolicy: policy,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateAtespaceAccessPolicy with an invalid atespace name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("UpdateAtespaceAccessPolicy error %q does not name the invalid field atespace.name", msg)
	}
}

func TestUpdateAtespaceAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	update := func(policy *ateapipb.AccessPolicy) error {
		toUpdate := proto.Clone(policy).(*ateapipb.AccessPolicy)
		toUpdate.Bindings = append(toUpdate.Bindings, &ateapipb.Binding{Role: "editor", Members: []string{harness.Member("carol")}})
		_, err := alice.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: toUpdate})
		return err
	}
	refusedUpdate := func(holding string, before *ateapipb.AccessPolicy) {
		t.Helper()
		if err := update(before); status.Code(err) != codes.PermissionDenied {
			t.Errorf("UpdateAtespaceAccessPolicy holding %s = %v, want PermissionDenied", holding, err)
		}
		after, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
		if err != nil {
			t.Fatalf("GetAtespaceAccessPolicy: %v", err)
		}
		if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
			t.Errorf("access policy after a refused update holding %s (-want +got):\n%s", holding, diff)
		}
	}

	refusedUpdate("no role", setAtespaceBinding(t, h, as, "viewer", harness.Member("bob")))
	missingPolicy := accessPolicySpec()
	missingPolicy.Metadata.Uid, missingPolicy.Metadata.Version = foreignUID, 1
	if _, err := alice.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}, AccessPolicy: missingPolicy}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("UpdateAtespaceAccessPolicy of a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	refusedUpdate("viewer", setAtespaceBinding(t, h, as, "viewer", harness.Member("alice")))
	refusedUpdate("editor", setAtespaceBinding(t, h, as, "editor", harness.Member("alice")))
	policy := setAtespaceBinding(t, h, as, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "UpdateAtespaceAccessPolicy holding owner", codes.OK, func() error { return update(policy) })
}

func TestUpdateAtespaceAccessPolicy_RevokesAccess(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
		return err
	}
	setAtespaceBinding(t, h, as, "viewer", harness.Member("alice"))
	eventuallyAllowed(t, h, "GetAtespace holding viewer", codes.OK, get)

	setAtespaceBinding(t, h, as, "viewer", harness.Member("bob"))
	h.Eventually("GetAtespace to be denied once the policy no longer names the principal", func() bool {
		return status.Code(get()) == codes.PermissionDenied
	})
}

func TestUpdateAtespaceAccessPolicy_DowngradesAccess(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	policy := setAtespaceBinding(t, h, as, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "UpdateAtespaceAccessPolicy holding owner", codes.OK, func() error {
		toUpdate := proto.Clone(policy).(*ateapipb.AccessPolicy)
		toUpdate.Bindings = append(toUpdate.Bindings, &ateapipb.Binding{Role: "viewer", Members: []string{harness.Member("carol")}})
		_, err := alice.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: toUpdate})
		return err
	})

	setAtespaceBinding(t, h, as, "viewer", harness.Member("alice"))
	before, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespace: %v", err)
	}
	h.Eventually("DeleteAtespace to be denied once the principal is downgraded to viewer", func() bool {
		_, err := alice.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: as})
		return status.Code(err) == codes.PermissionDenied
	})
	after, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespace after a refused delete: %v", err)
	}
	if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
		t.Errorf("atespace after a refused delete (-want +got):\n%s", diff)
	}
	if _, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as}); status.Code(err) != codes.OK {
		t.Errorf("GetAtespace holding viewer = %v, want OK", err)
	}
}

func TestUpdateAtespaceAccessPolicy_ManyMembers(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	var members []string
	var clients []ateapipb.ControlClient
	for i := range 250 {
		subject := fmt.Sprintf("member-%03d", i)
		members = append(members, harness.Member(subject))
		clients = append(clients, h.ClientAs(subject))
	}
	get := func(c ateapipb.ControlClient) error {
		_, err := c.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
		return err
	}
	policy, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: &ateapipb.AccessPolicy{
		Metadata: &ateapipb.ResourceMetadata{Name: "default"},
		Bindings: []*ateapipb.Binding{{Role: "viewer", Members: members}},
	}})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}
	for i, c := range clients {
		eventuallyAllowed(t, h, fmt.Sprintf("GetAtespace as %s", members[i]), codes.OK, func() error { return get(c) })
	}

	toUpdate := proto.Clone(policy).(*ateapipb.AccessPolicy)
	toUpdate.Bindings = []*ateapipb.Binding{{Role: "viewer", Members: members[:1]}}
	if _, err := h.Control.UpdateAtespaceAccessPolicy(t.Context(), &ateapipb.UpdateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: toUpdate}); err != nil {
		t.Fatalf("UpdateAtespaceAccessPolicy: %v", err)
	}
	if err := get(clients[0]); status.Code(err) != codes.OK {
		t.Errorf("GetAtespace as %s, still named = %v, want OK", members[0], err)
	}
	for i, c := range clients[1:] {
		h.Eventually(fmt.Sprintf("GetAtespace as %s to be denied once removed", members[i+1]), func() bool {
			return status.Code(get(c)) == codes.PermissionDenied
		})
	}
}

func TestDeleteAtespaceAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}

	deleted, err := h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("DeleteAtespaceAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(created, deleted, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteAtespaceAccessPolicy response (-created +deleted):\n%s", diff)
	}
	if _, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as}); status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy after delete = %v, want NotFound", err)
	}
}

func TestDeleteAtespaceAccessPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}

	_, err := h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteAtespaceAccessPolicy for an atespace with none = %v, want NotFound", err)
	}
}

func TestDeleteAtespaceAccessPolicy_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	created, err := h.Control.CreateAtespaceAccessPolicy(t.Context(), &ateapipb.CreateAtespaceAccessPolicyRequest{Atespace: as, AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateAtespaceAccessPolicy: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()

	_, err = h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteAtespaceAccessPolicy with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteAtespaceAccessPolicy with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
	if err != nil {
		t.Fatalf("GetAtespaceAccessPolicy after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("access policy after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteAtespaceAccessPolicy with the right uid and version: %v", err)
	}
}

func TestDeleteAtespaceAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteAtespaceAccessPolicy with an invalid atespace name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("DeleteAtespaceAccessPolicy error %q does not name the invalid field atespace.name", msg)
	}
}

func TestDeleteAtespaceAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	remove := func() error {
		_, err := alice.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as})
		return err
	}
	refusedDelete := func(holding string, before *ateapipb.AccessPolicy) {
		t.Helper()
		if err := remove(); status.Code(err) != codes.PermissionDenied {
			t.Errorf("DeleteAtespaceAccessPolicy holding %s = %v, want PermissionDenied", holding, err)
		}
		after, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: as})
		if err != nil {
			t.Fatalf("GetAtespaceAccessPolicy after a refused delete holding %s: %v", holding, err)
		}
		if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
			t.Errorf("access policy after a refused delete holding %s (-want +got):\n%s", holding, diff)
		}
	}

	refusedDelete("no role", setAtespaceBinding(t, h, as, "viewer", harness.Member("bob")))
	if _, err := alice.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("DeleteAtespaceAccessPolicy of a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	refusedDelete("viewer", setAtespaceBinding(t, h, as, "viewer", harness.Member("alice")))
	refusedDelete("editor", setAtespaceBinding(t, h, as, "editor", harness.Member("alice")))
	setAtespaceBinding(t, h, as, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "DeleteAtespaceAccessPolicy holding owner", codes.OK, remove)
}

func TestDeleteAtespaceAccessPolicy_RevokesAccess(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
		return err
	}
	setAtespaceBinding(t, h, as, "viewer", harness.Member("alice"))
	eventuallyAllowed(t, h, "GetAtespace holding viewer", codes.OK, get)

	if _, err := h.Control.DeleteAtespaceAccessPolicy(t.Context(), &ateapipb.DeleteAtespaceAccessPolicyRequest{Atespace: as}); err != nil {
		t.Fatalf("DeleteAtespaceAccessPolicy: %v", err)
	}
	h.Eventually("GetAtespace to be denied once the policy is deleted", func() bool {
		return status.Code(get()) == codes.PermissionDenied
	})
}

func TestCreateGlobalAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	created, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}
	want := &ateapipb.AccessPolicy{
		Metadata: &ateapipb.ResourceMetadata{Name: "default"},
		Bindings: []*ateapipb.Binding{{Role: "viewer", Members: []string{harness.Member("alice")}}},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created global access policy (-want +got):\n%s", diff)
	}
}

func TestCreateGlobalAccessPolicy_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}

	_, err = h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateGlobalAccessPolicy when it exists = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if err != nil {
		t.Fatalf("GetGlobalAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("global access policy after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateGlobalAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	policy := accessPolicySpec()
	policy.Metadata.Name = "other"

	_, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: policy})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateGlobalAccessPolicy named %q = %v, want InvalidArgument", policy.GetMetadata().GetName(), err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "access_policy.metadata.name") {
		t.Errorf("CreateGlobalAccessPolicy error %q does not name the invalid field access_policy.metadata.name", msg)
	}
}

func TestCreateGlobalAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	alice := h.ClientAs("alice")
	create := func() error {
		_, err := alice.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
		return err
	}

	if err := create(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("CreateGlobalAccessPolicy holding no role = %v, want PermissionDenied", err)
	}
	if _, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{}); status.Code(err) != codes.NotFound {
		t.Errorf("GetGlobalAccessPolicy after a refused create = %v, want NotFound", err)
	}
	setGlobalBinding(t, h, "viewer", harness.Member("alice"))
	if err := create(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("CreateGlobalAccessPolicy holding viewer = %v, want PermissionDenied", err)
	}
	setGlobalBinding(t, h, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "CreateGlobalAccessPolicy holding owner", codes.AlreadyExists, create)
}

func TestGetGlobalAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}

	got, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if err != nil {
		t.Fatalf("GetGlobalAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetGlobalAccessPolicy (-created +got):\n%s", diff)
	}
}

func TestGetGlobalAccessPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetGlobalAccessPolicy before it is created = %v, want NotFound", err)
	}
}

func TestGetGlobalAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
		return err
	}

	if err := get(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetGlobalAccessPolicy holding no role, before the policy exists = %v, want PermissionDenied", err)
	}
	setGlobalBinding(t, h, "viewer", harness.Member("bob"))
	if err := get(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetGlobalAccessPolicy holding no role = %v, want PermissionDenied", err)
	}
	for _, role := range []string{"viewer", "owner"} {
		setGlobalBinding(t, h, role, harness.Member("alice"))
		eventuallyAllowed(t, h, fmt.Sprintf("GetGlobalAccessPolicy holding %s", role), codes.OK, get)
	}
}

func TestUpdateGlobalAccessPolicy(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	before, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}
	bindings := []*ateapipb.Binding{{Role: "owner", Members: []string{harness.Member("bob")}}}

	toUpdate := proto.Clone(before).(*ateapipb.AccessPolicy)
	toUpdate.Bindings = bindings
	updated, err := h.Control.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: toUpdate})
	if err != nil {
		t.Fatalf("UpdateGlobalAccessPolicy: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.AccessPolicy)
	want.Bindings = bindings
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated global access policy (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated global access policy has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if err != nil {
		t.Fatalf("GetGlobalAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetGlobalAccessPolicy after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateGlobalAccessPolicy_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	policy := accessPolicySpec()
	policy.Metadata.Uid, policy.Metadata.Version = foreignUID, 1

	_, err := h.Control.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: policy})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateGlobalAccessPolicy before it is created = %v, want NotFound", err)
	}
}

func TestUpdateGlobalAccessPolicy_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	before, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.AccessPolicy)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.Bindings = []*ateapipb.Binding{{Role: "owner", Members: []string{harness.Member("bob")}}}
		_, err := h.Control.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateGlobalAccessPolicy with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateGlobalAccessPolicy with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
	if err != nil {
		t.Fatalf("GetGlobalAccessPolicy: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("global access policy after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateGlobalAccessPolicy without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateGlobalAccessPolicy_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	before, err := h.Control.CreateGlobalAccessPolicy(t.Context(), &ateapipb.CreateGlobalAccessPolicyRequest{AccessPolicy: accessPolicySpec()})
	if err != nil {
		t.Fatalf("CreateGlobalAccessPolicy: %v", err)
	}
	toUpdate := proto.Clone(before).(*ateapipb.AccessPolicy)
	toUpdate.Metadata.Name = "other"

	_, err = h.Control.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: toUpdate})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateGlobalAccessPolicy named %q = %v, want InvalidArgument", toUpdate.GetMetadata().GetName(), err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "access_policy.metadata.name") {
		t.Errorf("UpdateGlobalAccessPolicy error %q does not name the invalid field access_policy.metadata.name", msg)
	}
}

func TestUpdateGlobalAccessPolicy_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	alice := h.ClientAs("alice")
	update := func(policy *ateapipb.AccessPolicy) error {
		toUpdate := proto.Clone(policy).(*ateapipb.AccessPolicy)
		toUpdate.Bindings = append(toUpdate.Bindings, &ateapipb.Binding{Role: "viewer", Members: []string{harness.Member("carol")}})
		_, err := alice.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: toUpdate})
		return err
	}
	refusedUpdate := func(holding string, before *ateapipb.AccessPolicy) {
		t.Helper()
		if err := update(before); status.Code(err) != codes.PermissionDenied {
			t.Errorf("UpdateGlobalAccessPolicy holding %s = %v, want PermissionDenied", holding, err)
		}
		after, err := h.Control.GetGlobalAccessPolicy(t.Context(), &ateapipb.GetGlobalAccessPolicyRequest{})
		if err != nil {
			t.Fatalf("GetGlobalAccessPolicy: %v", err)
		}
		if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
			t.Errorf("global access policy after a refused update holding %s (-want +got):\n%s", holding, diff)
		}
	}

	missingPolicy := accessPolicySpec()
	missingPolicy.Metadata.Uid, missingPolicy.Metadata.Version = foreignUID, 1
	if _, err := alice.UpdateGlobalAccessPolicy(t.Context(), &ateapipb.UpdateGlobalAccessPolicyRequest{AccessPolicy: missingPolicy}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("UpdateGlobalAccessPolicy before the policy exists holding no role = %v, want PermissionDenied", err)
	}
	refusedUpdate("no role", setGlobalBinding(t, h, "viewer", harness.Member("bob")))
	refusedUpdate("viewer", setGlobalBinding(t, h, "viewer", harness.Member("alice")))
	policy := setGlobalBinding(t, h, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "UpdateGlobalAccessPolicy holding owner", codes.OK, func() error { return update(policy) })
}

func TestUpdateGlobalAccessPolicy_RevokesInheritedAccess(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: as})
		return err
	}
	setGlobalBinding(t, h, "viewer", harness.Member("alice"))
	eventuallyAllowed(t, h, "GetAtespace holding the global viewer role", codes.OK, get)

	setGlobalBinding(t, h, "viewer", harness.Member("bob"))
	h.Eventually("GetAtespace to be denied once the global policy no longer names the principal", func() bool {
		return status.Code(get()) == codes.PermissionDenied
	})
}
