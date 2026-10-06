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

// templateSpec is a valid ActorTemplate named name in atespace.
func templateSpec(atespace, name string) *ateapipb.ActorTemplate {
	return &ateapipb.ActorTemplate{
		Metadata:       &ateapipb.ResourceMetadata{Atespace: atespace, Name: name},
		SnapshotConfig: &ateapipb.SnapshotConfig{StorageLocation: harness.StorageLocation},
		SandboxConfig: &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_GVISOR,
			ConfigName:   harness.SandboxConfigName,
		},
		Containers:     []*ateapipb.Container{{Name: "main", Image: harness.Image, Command: []string{"/main"}}},
		WorkerSelector: &ateapipb.Selector{MatchLabels: map[string]string{"tier": "1"}},
	}
}

func TestCreateActorTemplate(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	created, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}
	want := &ateapipb.ActorTemplate{
		Metadata: &ateapipb.ResourceMetadata{Atespace: as, Name: "tmpl-a"},
		SnapshotConfig: &ateapipb.SnapshotConfig{
			StorageLocation: harness.StorageLocation,
			OnPause:         ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL,
			OnCommit:        ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL,
		},
		SandboxConfig: &ateapipb.SandboxConfig{
			SandboxClass: ateapipb.SandboxClass_SANDBOX_CLASS_GVISOR,
			ConfigName:   harness.SandboxConfigName,
		},
		Containers:     []*ateapipb.Container{{Name: "main", Image: harness.Image, Command: []string{"/main"}}},
		WorkerSelector: &ateapipb.Selector{MatchLabels: map[string]string{"tier": "1"}},
		Status:         &ateapipb.ActorTemplateStatus{},
	}
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata); diff != "" {
		t.Errorf("created actor template (-want +got):\n%s", diff)
	}
}

func TestCreateActorTemplate_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	created, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	_, err = h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateActorTemplate of an existing name = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-a"}})
	if err != nil {
		t.Fatalf("GetActorTemplate: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("actor template after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateActorTemplate_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "Not_A_Valid_Name")})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateActorTemplate with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor_template.metadata.name") {
		t.Errorf("CreateActorTemplate error %q does not name the invalid field actor_template.metadata.name", msg)
	}
}

func TestCreateActorTemplate_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec("team-missing", "tmpl-a")})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("CreateActorTemplate in a missing atespace = %v, want FailedPrecondition", err)
	}
	listed, err := h.Control.ListActorTemplates(t.Context(), &ateapipb.ListActorTemplatesRequest{})
	if err != nil {
		t.Fatalf("ListActorTemplates: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorTemplatesResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("actor templates after a refused create (-want +got):\n%s", diff)
	}
}

func TestGetActorTemplate(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	created, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	got, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-a"}})
	if err != nil {
		t.Fatalf("GetActorTemplate: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetActorTemplate (-created +got):\n%s", diff)
	}
}

func TestGetActorTemplate_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetActorTemplate of a missing template = %v, want NotFound", err)
	}
}

func TestGetActorTemplate_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetActorTemplate with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor_template.name") {
		t.Errorf("GetActorTemplate error %q does not name the invalid field actor_template.name", msg)
	}
}

func TestListActorTemplates(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	other := h.CreateAtespace("team-b")
	const n, pageSize = 5, 2
	var created []*ateapipb.ActorTemplate
	for i := range n {
		tmpl, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, fmt.Sprintf("tmpl-%d", i))})
		if err != nil {
			t.Fatalf("CreateActorTemplate: %v", err)
		}
		created = append(created, tmpl)
	}
	if _, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(other, "tmpl-other")}); err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	var listed []*ateapipb.ActorTemplate
	req := &ateapipb.ListActorTemplatesRequest{Atespace: as, PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListActorTemplates(t.Context(), req)
		if err != nil {
			t.Fatalf("ListActorTemplates page %d: %v", pages, err)
		}
		if got := len(resp.GetActorTemplates()); got > pageSize {
			t.Errorf("page %d holds %d templates, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetActorTemplates()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byName := cmpopts.SortSlices(func(a, b *ateapipb.ActorTemplate) bool { return a.GetMetadata().GetName() < b.GetMetadata().GetName() })
	if diff := cmp.Diff(created, listed, protocmp.Transform(), byName); diff != "" {
		t.Errorf("listed actor templates (-want +got):\n%s", diff)
	}
}

func TestListActorTemplates_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListActorTemplates(t.Context(), &ateapipb.ListActorTemplatesRequest{PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListActorTemplates with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListActorTemplates error %q does not name the invalid field page_size", msg)
	}
}

func TestListActorTemplates_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListActorTemplates(t.Context(), &ateapipb.ListActorTemplatesRequest{PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListActorTemplates with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestListActorTemplates_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	listed, err := h.Control.ListActorTemplates(t.Context(), &ateapipb.ListActorTemplatesRequest{Atespace: "team-missing"})
	if err != nil {
		t.Fatalf("ListActorTemplates in a missing atespace: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListActorTemplatesResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("ListActorTemplates in a missing atespace (-want +got):\n%s", diff)
	}
}

func TestDeleteActorTemplate(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-a"}
	created, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}

	deleted, err := h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: ref})
	if err != nil {
		t.Fatalf("DeleteActorTemplate: %v", err)
	}
	if diff := cmp.Diff(created, deleted, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteActorTemplate response (-created +deleted):\n%s", diff)
	}
	if _, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetActorTemplate after delete = %v, want NotFound", err)
	}
}

func TestDeleteActorTemplate_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteActorTemplate of a missing template = %v, want NotFound", err)
	}
}

func TestDeleteActorTemplate_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tmpl-a"}
	created, err := h.Control.CreateActorTemplate(t.Context(), &ateapipb.CreateActorTemplateRequest{ActorTemplate: templateSpec(as, "tmpl-a")})
	if err != nil {
		t.Fatalf("CreateActorTemplate: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()

	_, err = h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: ref, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActorTemplate with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteActorTemplate with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetActorTemplate(t.Context(), &ateapipb.GetActorTemplateRequest{ActorTemplate: ref})
	if err != nil {
		t.Fatalf("GetActorTemplate after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("actor template after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteActorTemplate with the right uid and version: %v", err)
	}
}

func TestDeleteActorTemplate_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteActorTemplate(t.Context(), &ateapipb.DeleteActorTemplateRequest{ActorTemplate: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteActorTemplate with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "actor_template.name") {
		t.Errorf("DeleteActorTemplate error %q does not name the invalid field actor_template.name", msg)
	}
}
