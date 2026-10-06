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
	"google.golang.org/protobuf/testing/protocmp"
)

func TestCreateAtespace(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}
	want := &ateapipb.Atespace{Metadata: &ateapipb.ResourceMetadata{Name: "team-a"}}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created atespace (-want +got):\n%s", diff)
	}
}

func TestCreateAtespace_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}

	_, err = h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateAtespace of an existing name = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-a"}})
	if err != nil {
		t.Fatalf("GetAtespace: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("atespace after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateAtespace_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "Not_A_Valid_Name"},
	}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateAtespace with an invalid name = %v, want InvalidArgument", err)
	}
	listed, err := h.Control.ListAtespaces(t.Context(), &ateapipb.ListAtespacesRequest{})
	if err != nil {
		t.Fatalf("ListAtespaces: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListAtespacesResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("atespaces after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateAtespace_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: "team-a"}
	alice := h.ClientAs("alice")
	create := func() error {
		_, err := alice.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
			Metadata: &ateapipb.ResourceMetadata{Name: ref.GetName()},
		}})
		return err
	}
	refusedCreate := func(holding string) {
		t.Helper()
		if err := create(); status.Code(err) != codes.PermissionDenied {
			t.Errorf("CreateAtespace holding %s = %v, want PermissionDenied", holding, err)
		}
		if _, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref}); status.Code(err) != codes.NotFound {
			t.Errorf("GetAtespace after a refused create holding %s = %v, want NotFound", holding, err)
		}
	}

	refusedCreate("no role")
	setGlobalBinding(t, h, "viewer", harness.Member("alice"))
	refusedCreate("viewer")
	setGlobalBinding(t, h, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "CreateAtespace holding owner", codes.OK, create)
}

func TestGetAtespace(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}

	got, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-a"}})
	if err != nil {
		t.Fatalf("GetAtespace: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetAtespace (-created +got):\n%s", diff)
	}
}

func TestGetAtespace_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespace of a missing atespace = %v, want NotFound", err)
	}
}

func TestGetAtespace_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetAtespace with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("GetAtespace error %q does not name the invalid field atespace.name", msg)
	}
}

func TestGetAtespace_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
		return err
	}

	if err := get(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetAtespace holding no role = %v, want PermissionDenied", err)
	}
	if _, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("GetAtespace of a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	for _, role := range []string{"viewer", "editor", "owner"} {
		setAtespaceBinding(t, h, ref, role, harness.Member("alice"))
		eventuallyAllowed(t, h, fmt.Sprintf("GetAtespace holding %s", role), codes.OK, get)
	}
}

func TestGetAtespace_AtespaceRecreated(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	get := func() error {
		_, err := alice.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
		return err
	}
	setAtespaceBinding(t, h, ref, "viewer", harness.Member("alice"))
	eventuallyAllowed(t, h, "GetAtespace holding viewer", codes.OK, get)

	if _, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref}); err != nil {
		t.Fatalf("DeleteAtespace: %v", err)
	}
	h.CreateAtespace(ref.GetName())
	h.Eventually("GetAtespace of the recreated atespace to be denied", func() bool {
		return status.Code(get()) == codes.PermissionDenied
	})
	if _, err := h.Control.GetAtespaceAccessPolicy(t.Context(), &ateapipb.GetAtespaceAccessPolicyRequest{Atespace: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespaceAccessPolicy of the recreated atespace = %v, want NotFound", err)
	}
}

func TestListAtespaces(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	const n, pageSize = 5, 2
	var created []*ateapipb.Atespace
	for i := range n {
		atespace, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
			Metadata: &ateapipb.ResourceMetadata{Name: fmt.Sprintf("team-%d", i)},
		}})
		if err != nil {
			t.Fatalf("CreateAtespace: %v", err)
		}
		created = append(created, atespace)
	}

	var listed []*ateapipb.Atespace
	req := &ateapipb.ListAtespacesRequest{PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListAtespaces(t.Context(), req)
		if err != nil {
			t.Fatalf("ListAtespaces page %d: %v", pages, err)
		}
		if got := len(resp.GetAtespaces()); got > pageSize {
			t.Errorf("page %d holds %d atespaces, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetAtespaces()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byName := cmpopts.SortSlices(func(a, b *ateapipb.Atespace) bool { return a.GetMetadata().GetName() < b.GetMetadata().GetName() })
	if diff := cmp.Diff(created, listed, protocmp.Transform(), byName); diff != "" {
		t.Errorf("listed atespaces (-want +got):\n%s", diff)
	}
}

func TestListAtespaces_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListAtespaces(t.Context(), &ateapipb.ListAtespacesRequest{PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListAtespaces with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListAtespaces error %q does not name the invalid field page_size", msg)
	}
}

func TestListAtespaces_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListAtespaces(t.Context(), &ateapipb.ListAtespacesRequest{PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListAtespaces with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestListAtespaces_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	h.CreateAtespace("team-a")
	alice := h.ClientAs("alice")
	list := func() error {
		_, err := alice.ListAtespaces(t.Context(), &ateapipb.ListAtespacesRequest{})
		return err
	}

	if err := list(); status.Code(err) != codes.PermissionDenied {
		t.Errorf("ListAtespaces holding no role = %v, want PermissionDenied", err)
	}
	for _, role := range []string{"viewer", "owner"} {
		setGlobalBinding(t, h, role, harness.Member("alice"))
		eventuallyAllowed(t, h, fmt.Sprintf("ListAtespaces holding %s", role), codes.OK, list)
	}
}

func TestDeleteAtespace(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: "team-a"}
	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}

	deleted, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref})
	if err != nil {
		t.Fatalf("DeleteAtespace: %v", err)
	}
	if diff := cmp.Diff(created, deleted, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteAtespace response (-created +deleted):\n%s", diff)
	}
	if _, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetAtespace after delete = %v, want NotFound", err)
	}
}

func TestDeleteAtespace_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteAtespace of a missing atespace = %v, want NotFound", err)
	}
}

func TestDeleteAtespace_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: "team-a"}
	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()
	const wrongUID = "0f0e0d0c-0b0a-4908-8706-050403020100"

	_, err = h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref, Options: &ateapipb.DeleteOptions{Uid: wrongUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteAtespace with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteAtespace with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
	if err != nil {
		t.Fatalf("GetAtespace after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("atespace after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Fatalf("DeleteAtespace with the right uid and version: %v", err)
	}
	_, err = h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteAtespace after delete = %v, want NotFound", err)
	}
}

func TestDeleteAtespace_HasChildren(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: "team-a"}
	created, err := h.Control.CreateAtespace(t.Context(), &ateapipb.CreateAtespaceRequest{Atespace: &ateapipb.Atespace{
		Metadata: &ateapipb.ResourceMetadata{Name: "team-a"},
	}})
	if err != nil {
		t.Fatalf("CreateAtespace: %v", err)
	}
	if _, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: &ateapipb.ActorTemplate{
		Metadata:       &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "tmpl-a"},
		SnapshotConfig: &ateapipb.SnapshotConfig{StorageLocation: harness.StorageLocation},
		SandboxConfig: &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_GVISOR,
			ConfigName:   harness.SandboxConfigName,
		},
		Containers:     []*ateapipb.Container{{Name: "main", Image: harness.Image, Command: []string{"/main"}}},
		WorkerSelector: &ateapipb.Selector{MatchLabels: map[string]string{"pool": "pool1"}},
	}}); err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	_, err = h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("DeleteAtespace holding an actor template = %v, want FailedPrecondition", err)
	}
	got, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
	if err != nil {
		t.Fatalf("GetAtespace after a refused delete: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("atespace after a refused delete (-want +got):\n%s", diff)
	}
}

func TestDeleteAtespace_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteAtespace with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "atespace.name") {
		t.Errorf("DeleteAtespace error %q does not name the invalid field atespace.name", msg)
	}
}

func TestDeleteAtespace_Authorization(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	ref := &ateapipb.ObjectRef{Name: h.CreateAtespace("team-a")}
	alice := h.ClientAs("alice")
	refusedDelete := func(holding string) {
		t.Helper()
		before, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
		if err != nil {
			t.Fatalf("GetAtespace: %v", err)
		}
		if _, err := alice.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref}); status.Code(err) != codes.PermissionDenied {
			t.Errorf("DeleteAtespace holding %s = %v, want PermissionDenied", holding, err)
		}
		after, err := h.Control.GetAtespace(t.Context(), &ateapipb.GetAtespaceRequest{Atespace: ref})
		if err != nil {
			t.Fatalf("GetAtespace after a refused delete holding %s: %v", holding, err)
		}
		if diff := cmp.Diff(before, after, protocmp.Transform()); diff != "" {
			t.Errorf("atespace after a refused delete holding %s (-want +got):\n%s", holding, diff)
		}
	}

	refusedDelete("no role")
	if _, err := alice.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: &ateapipb.ObjectRef{Name: "team-missing"}}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("DeleteAtespace of a missing atespace holding no role = %v, want PermissionDenied", err)
	}
	for _, role := range []string{"viewer", "editor"} {
		setAtespaceBinding(t, h, ref, role, harness.Member("alice"))
		refusedDelete(role)
	}
	setAtespaceBinding(t, h, ref, "owner", harness.Member("alice"))
	eventuallyAllowed(t, h, "DeleteAtespace holding owner", codes.OK, func() error {
		_, err := alice.DeleteAtespace(t.Context(), &ateapipb.DeleteAtespaceRequest{Atespace: ref})
		return err
	})
}
