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

func TestCreateTag(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	tmpl := h.NewTemplate(as, pool)
	source := harness.ActorRef(h.SuspendedActor(as, tmpl))

	created, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	want := &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
		Status: &ateapipb.TagStatus{
			Snapshot: &ateapipb.ExternalSnapshot{
				ContentScope: ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL,
			},
			ActorTemplateUid: tmpl.GetMetadata().GetUid(),
			StorageLocation:  harness.StorageLocation,
		},
	}
	// The tag's snapshot is named by ate-api; it only has to hold the copy.
	ignoreSnapshotURI := protocmp.IgnoreFields(&ateapipb.ExternalSnapshot{}, "snapshot_uri")
	if diff := cmp.Diff(want, created, protocmp.Transform(), ignoreServerMetadata, ignoreSnapshotURI); diff != "" {
		t.Errorf("created tag (-want +got):\n%s", diff)
	}
	uri := created.GetStatus().GetSnapshot().GetSnapshotUri()
	if names, err := h.ObjectStore.Snapshot(uri); err != nil || len(names) == 0 {
		t.Errorf("tag's snapshot %q in object storage = %v, %v; want it present", uri, names, err)
	}
}

func TestCreateTag_AlreadyExists(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	tag := &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}
	created, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: tag})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	_, err = h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: tag})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("CreateTag of an existing name = %v, want AlreadyExists", err)
	}
	got, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("tag after a refused create (-want +got):\n%s", diff)
	}
}

func TestCreateTag_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "Not_A_Valid_Name"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-a"},
	}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateTag with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "tag.metadata.name") {
		t.Errorf("CreateTag error %q does not name the invalid field tag.metadata.name", msg)
	}
}

func TestCreateTag_SourceActorNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-missing"},
	}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("CreateTag from a missing source actor = %v, want NotFound", err)
	}
	listed, err := h.Control.ListTags(t.Context(), &ateapipb.ListTagsRequest{Atespace: as})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListTagsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("tags after a refused create (-want +got):\n%s", diff)
	}
}

func TestGetTag(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	created, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	got, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetTag (-created +got):\n%s", diff)
	}
}

func TestGetTag_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "tag-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetTag of a missing tag = %v, want NotFound", err)
	}
}

func TestGetTag_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("GetTag with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "tag.name") {
		t.Errorf("GetTag error %q does not name the invalid field tag.name", msg)
	}
}

func TestListTags(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	other := h.CreateAtespace("team-b")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	otherSource := harness.ActorRef(h.SuspendedActor(other, h.NewTemplate(other, pool)))
	const n, pageSize = 5, 2
	var created []*ateapipb.Tag
	for i := range n {
		tag, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
			Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: fmt.Sprintf("tag-%d", i)},
			Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
			SourceActor: source,
		}})
		if err != nil {
			t.Fatalf("CreateTag: %v", err)
		}
		created = append(created, tag)
	}
	if _, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: other, Name: "tag-other"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: otherSource,
	}}); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	var listed []*ateapipb.Tag
	req := &ateapipb.ListTagsRequest{Atespace: as, PageSize: pageSize}
	for pages := 1; ; pages++ {
		if pages > n {
			t.Fatalf("listing took more than %d pages", n)
		}
		resp, err := h.Control.ListTags(t.Context(), req)
		if err != nil {
			t.Fatalf("ListTags page %d: %v", pages, err)
		}
		if got := len(resp.GetTags()); got > pageSize {
			t.Errorf("page %d holds %d tags, want at most %d", pages, got, pageSize)
		}
		listed = append(listed, resp.GetTags()...)
		if resp.GetNextPageToken() == "" {
			break
		}
		req.PageToken = resp.GetNextPageToken()
	}
	byName := cmpopts.SortSlices(func(a, b *ateapipb.Tag) bool { return a.GetMetadata().GetName() < b.GetMetadata().GetName() })
	if diff := cmp.Diff(created, listed, protocmp.Transform(), byName); diff != "" {
		t.Errorf("listed tags (-want +got):\n%s", diff)
	}
}

func TestListTags_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListTags(t.Context(), &ateapipb.ListTagsRequest{PageSize: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListTags with a negative page size = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "page_size") {
		t.Errorf("ListTags error %q does not name the invalid field page_size", msg)
	}
}

func TestListTags_InvalidPageToken(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	_, err := h.Control.ListTags(t.Context(), &ateapipb.ListTagsRequest{PageToken: "not-a-real-token"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListTags with a malformed page token = %v, want InvalidArgument", err)
	}
}

func TestListTags_AtespaceNotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	listed, err := h.Control.ListTags(t.Context(), &ateapipb.ListTagsRequest{Atespace: "team-missing"})
	if err != nil {
		t.Fatalf("ListTags in a missing atespace: %v", err)
	}
	if diff := cmp.Diff(&ateapipb.ListTagsResponse{}, listed, protocmp.Transform()); diff != "" {
		t.Errorf("ListTags in a missing atespace (-want +got):\n%s", diff)
	}
}

func TestUpdateTag(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}
	before, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	toUpdate := proto.Clone(before).(*ateapipb.Tag)
	toUpdate.Scope = ateapipb.TagScope_TAG_SCOPE_PUBLISHED
	updated, err := h.Control.UpdateTag(t.Context(), &ateapipb.UpdateTagRequest{Tag: toUpdate})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	want := proto.Clone(before).(*ateapipb.Tag)
	want.Scope = ateapipb.TagScope_TAG_SCOPE_PUBLISHED
	if diff := cmp.Diff(want, updated, protocmp.Transform(), ignoreVersion, ignoreTimestamps); diff != "" {
		t.Errorf("updated tag (-want +got):\n%s", diff)
	}
	if got, was := updated.GetMetadata().GetVersion(), before.GetMetadata().GetVersion(); got <= was {
		t.Errorf("updated tag has version %d, want higher than %d", got, was)
	}
	got, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: ref})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if diff := cmp.Diff(updated, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetTag after update (-updated +got):\n%s", diff)
	}
}

func TestUpdateTag_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.UpdateTag(t.Context(), &ateapipb.UpdateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-missing", Uid: foreignUID, Version: 1},
		Scope:       ateapipb.TagScope_TAG_SCOPE_PUBLISHED,
		SourceActor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-a"},
	}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateTag of a missing tag = %v, want NotFound", err)
	}
}

func TestUpdateTag_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}
	before, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	update := func(uid string, version int64) error {
		toUpdate := proto.Clone(before).(*ateapipb.Tag)
		toUpdate.Metadata.Uid, toUpdate.Metadata.Version = uid, version
		toUpdate.Scope = ateapipb.TagScope_TAG_SCOPE_PUBLISHED
		_, err := h.Control.UpdateTag(t.Context(), &ateapipb.UpdateTagRequest{Tag: toUpdate})
		return err
	}

	if err := update(foreignUID, before.GetMetadata().GetVersion()); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateTag with the wrong uid = %v, want Aborted", err)
	}
	if err := update(before.GetMetadata().GetUid(), before.GetMetadata().GetVersion()+1); status.Code(err) != codes.Aborted {
		t.Errorf("UpdateTag with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: ref})
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if diff := cmp.Diff(before, got, protocmp.Transform()); diff != "" {
		t.Errorf("tag after refused updates (-want +got):\n%s", diff)
	}
	if err := update("", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateTag without a uid or version = %v, want InvalidArgument", err)
	}
}

func TestUpdateTag_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.UpdateTag(t.Context(), &ateapipb.UpdateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "Not_A_Valid_Name", Uid: foreignUID, Version: 1},
		Scope:       ateapipb.TagScope_TAG_SCOPE_PUBLISHED,
		SourceActor: &ateapipb.ObjectRef{Atespace: as, Name: "actor-a"},
	}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("UpdateTag with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "tag.metadata.name") {
		t.Errorf("UpdateTag error %q does not name the invalid field tag.metadata.name", msg)
	}
}

func TestDeleteTag(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}
	created, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	deleted, err := h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: ref})
	if err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if diff := cmp.Diff(created, deleted, protocmp.Transform()); diff != "" {
		t.Errorf("DeleteTag response (-created +deleted):\n%s", diff)
	}
	if _, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: ref}); status.Code(err) != codes.NotFound {
		t.Errorf("GetTag after delete = %v, want NotFound", err)
	}
}

func TestDeleteTag_NotFound(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "tag-missing"}})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteTag of a missing tag = %v, want NotFound", err)
	}
}

func TestDeleteTag_WithPreconditions(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")
	pool := h.CreateWorkerPool("pool1", nil)
	h.AddWorker(pool, h.Node("a"))
	source := harness.ActorRef(h.SuspendedActor(as, h.NewTemplate(as, pool)))
	ref := &ateapipb.ObjectRef{Atespace: as, Name: "tag-a"}
	created, err := h.Control.CreateTag(t.Context(), &ateapipb.CreateTagRequest{Tag: &ateapipb.Tag{
		Metadata:    &ateapipb.ResourceMetadata{Atespace: as, Name: "tag-a"},
		Scope:       ateapipb.TagScope_TAG_SCOPE_ATESPACE,
		SourceActor: source,
	}})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	uid, version := created.GetMetadata().GetUid(), created.GetMetadata().GetVersion()

	_, err = h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: ref, Options: &ateapipb.DeleteOptions{Uid: foreignUID}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteTag with the wrong uid = %v, want Aborted", err)
	}
	_, err = h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version + 1}})
	if status.Code(err) != codes.Aborted {
		t.Errorf("DeleteTag with the right uid and the wrong version = %v, want Aborted", err)
	}
	got, err := h.Control.GetTag(t.Context(), &ateapipb.GetTagRequest{Tag: ref})
	if err != nil {
		t.Fatalf("GetTag after refused deletes: %v", err)
	}
	if diff := cmp.Diff(created, got, protocmp.Transform()); diff != "" {
		t.Errorf("tag after refused deletes (-want +got):\n%s", diff)
	}

	if _, err := h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: ref, Options: &ateapipb.DeleteOptions{Uid: uid, Version: version}}); err != nil {
		t.Errorf("DeleteTag with the right uid and version: %v", err)
	}
}

func TestDeleteTag_ValidationError(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	as := h.CreateAtespace("team-a")

	_, err := h.Control.DeleteTag(t.Context(), &ateapipb.DeleteTagRequest{Tag: &ateapipb.ObjectRef{Atespace: as, Name: "Not_A_Valid_Name"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("DeleteTag with an invalid name = %v, want InvalidArgument", err)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "tag.name") {
		t.Errorf("DeleteTag error %q does not name the invalid field tag.name", msg)
	}
}
