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

package controlapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store"
	"github.com/agent-substrate/substrate/cmd/ateapi/internal/validation"
	"github.com/agent-substrate/substrate/internal/objectstore"
	"github.com/agent-substrate/substrate/internal/resources"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// CreateTag tags the external snapshot a suspended Actor holds, giving the
// tag its own copy of that snapshot so the Actor being suspended
// again or deleted cannot collect it. The work is a workflow because it spans
// two transactions around an object copy; see TagActorSnapshot.
func (s *RPCService) CreateTag(ctx context.Context, req *ateapipb.CreateTagRequest) (*ateapipb.Tag, error) {
	// First scrub any fields that users are not allowed to set.
	inTag := req.Tag
	if inTag != nil { // otherwise validation will flag it
		scrubResourceMetadataForCreate(inTag.Metadata)
		inTag.Status = nil
	}

	if errs := validation.ValidateCreateTagRequest(ctx, req); len(errs) > 0 {
		return nil, toGRPCStatusError(errs)
	}
	actorRef := resources.ActorRefFromObjectRef(req.GetTag().GetSourceActor())
	setSpanActorRefAttributes(ctx, actorRef)

	tag, err := s.actorWorkflow.TagActorSnapshot(ctx, req.GetTag())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Actor %s not found", actorRef)
		}
		return nil, err
	}
	return tag, nil
}

func (s *ServiceImpl) CreateTag(ctx context.Context, tag *ateapipb.Tag) (*ateapipb.Tag, error) {
	// TODO: implement this
	return s.store.CreateTag(ctx, tag)
}

func (s *RPCService) GetTag(ctx context.Context, req *ateapipb.GetTagRequest) (*ateapipb.Tag, error) {
	if errs := validation.ValidateGetTagRequest(ctx, req); len(errs) > 0 {
		return nil, toGRPCStatusError(errs)
	}
	tag, err := s.impl.GetTag(ctx, resources.TagRefFromObjectRef(req.GetTag()))
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "Tag not found")
	}
	if err != nil {
		return nil, fmt.Errorf("while getting tag: %w", err)
	}
	return tag, nil
}

func (s *ServiceImpl) GetTag(ctx context.Context, tagRef resources.TagRef) (*ateapipb.Tag, error) {
	// TODO: implement this
	return s.store.GetTag(ctx, tagRef)
}

func (s *RPCService) ListTags(ctx context.Context, req *ateapipb.ListTagsRequest) (*ateapipb.ListTagsResponse, error) {
	if errs := validation.ValidateListTagsRequest(ctx, req); len(errs) > 0 {
		return nil, toGRPCStatusError(errs)
	}
	page, err := s.impl.ListTags(ctx, req.GetAtespace(), store.ListOptions{PageSize: effectivePageSize(req.GetPageSize()), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, mapListError(fmt.Errorf("while listing tags: %w", err))
	}
	return &ateapipb.ListTagsResponse{Tags: page.Items, NextPageToken: page.NextPageToken}, nil
}

func (s *ServiceImpl) ListTags(ctx context.Context, atespace string, opts store.ListOptions) (store.ListResponse[*ateapipb.Tag], error) {
	// TODO: implement this
	return s.store.ListTags(ctx, atespace, opts)
}

// errTagPending is what the update's mutate closure returns when the stored
// tag's create never finished, so the caller can tell it apart from a store
// failure and answer FAILED_PRECONDITION.
var errTagPending = errors.New("tag is still being created")

func (s *RPCService) UpdateTag(ctx context.Context, req *ateapipb.UpdateTagRequest) (*ateapipb.Tag, error) {
	// First scrub any fields that users are not allowed to set.
	inTag := req.Tag
	if inTag != nil { // otherwise validation will flag it
		scrubResourceMetadataForUpdate(inTag.Metadata)
		inTag.Status = nil
	}

	if errs := validation.ValidateUpdateTagRequest(ctx, req); len(errs) > 0 {
		return nil, toGRPCStatusError(errs)
	}
	in := req.GetTag()
	tagRef := resources.TagRefFromTag(in)

	storedTag, err := s.impl.UpdateTag(ctx, tagRef, store.PreconditionFrom(in), func(toUpdate *ateapipb.Tag) error {
		// A tag whose create never finished names a partial copy. Publishing it
		// — or changing its scope at all — would hand out content that is still
		// being written, or may never be.
		if toUpdate.GetStatus().GetSnapshot().GetSnapshotUri() == "" {
			return errTagPending
		}
		// Metadata and status are server-owned fields.
		metadata, tagStatus := toUpdate.GetMetadata(), toUpdate.GetStatus()
		// Whole-object replace: clear first, so a field the client left unset is
		// cleared rather than kept from the stored tag. Merge cannot smuggle in
		// unknown fields because validation already rejected them, and a source
		// the client did not echo back is caught by the immutability check the
		// impl runs on the merged tag: a tag never moves between snapshots, so
		// it never moves between sources either.
		proto.Reset(toUpdate)
		proto.Merge(toUpdate, in)
		// Restore the server-owned fields, discarding whatever the request
		// carried in them.
		toUpdate.Metadata, toUpdate.Status = metadata, tagStatus
		return nil
	})
	if err != nil {
		if errors.Is(err, errTagPending) {
			return nil, status.Errorf(codes.FailedPrecondition, "Tag %s/%s is still being created", tagRef.Atespace, tagRef.Name)
		}
		if errors.Is(err, store.ErrImmutableField) {
			return nil, status.Errorf(codes.InvalidArgument, "while updating tag %s/%s: %v", tagRef.Atespace, tagRef.Name, err)
		}
		if errors.Is(err, store.ErrVersionConflict) {
			return nil, status.Error(codes.Aborted, "concurrent update conflict, please retry")
		}
		if errors.Is(err, store.ErrUIDConflict) {
			return nil, status.Errorf(codes.Aborted, "Tag %s/%s not found with uid %s", tagRef.Atespace, tagRef.Name, in.GetMetadata().GetUid())
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Tag %s/%s not found", tagRef.Atespace, tagRef.Name)
		}
		if errors.Is(err, store.ErrPreconditionRequired) {
			return nil, status.Errorf(codes.InvalidArgument, "while updating tag %s/%s: %v", tagRef.Atespace, tagRef.Name, err)
		}
		return nil, fmt.Errorf("while updating tag: %w", err)
	}
	return storedTag, nil
}

func (s *ServiceImpl) UpdateTag(ctx context.Context, tagRef resources.TagRef, precondition store.Precondition, mutate func(toUpdate *ateapipb.Tag) error) (*ateapipb.Tag, error) {
	return s.store.UpdateTag(ctx, tagRef, precondition, func(toUpdate *ateapipb.Tag) error {
		// Apply the mutation function to the stored value.
		oldVal := proto.CloneOf(toUpdate)
		if err := mutate(toUpdate); err != nil {
			return err
		}

		// Validate the merged tag against the one it replaces. This is where
		// the rules the request could not be checked against land: scope, and
		// the immutability of metadata and source_actor.
		if errs := validation.ValidateTagUpdate(ctx, field.NewPath("tag"), toUpdate, oldVal); len(errs) > 0 {
			return toGRPCStatusError(errs)
		}
		return nil
	})
}

// DeleteTag releases the external snapshot the tag owns and then
// removes the row, in that order: the row is the only handle on that snapshot,
// so dropping it first would leak. A failure at any point fails the whole RPC;
// the client retries the same delete, which rediscovers the work from the row
// and resumes over whatever is left.
//
// The tag stays resolvable while its snapshot is being collected, so a
// CreateActor racing this delete can seed an Actor from content that is going
// away. That race is accepted for now.
//
// Note that this destroys the external snapshot: an Actor created from the tag
// and never suspended is still borrowing it and becomes unrecoverable. Do not
// delete a tag while clones of it exist.
func (s *RPCService) DeleteTag(ctx context.Context, req *ateapipb.DeleteTagRequest) (*ateapipb.Tag, error) {
	// TODO: mode delete orchestration to a workflow.
	if errs := validation.ValidateDeleteTagRequest(ctx, req); len(errs) > 0 {
		return nil, toGRPCStatusError(errs)
	}
	tagRef := resources.TagRefFromObjectRef(req.GetTag())

	// Serializes against a create of the same tag, whose copy would otherwise
	// keep writing into the prefix this is collecting.
	ctx, lease, err := acquireTagLease(ctx, s.impl, tagRef)
	if err != nil {
		return nil, err
	}
	defer lease.Close()

	stored, err := s.impl.GetTag(ctx, tagRef)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "Tag %s/%s not found", tagRef.Atespace, tagRef.Name)
		}
		return nil, fmt.Errorf("while getting tag: %w", err)
	}
	if err := s.releaseTagSnapshot(ctx, stored); err != nil {
		return nil, err
	}

	tag, err := s.impl.DeleteTag(ctx, tagRef)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "Tag %s/%s not found", tagRef.Atespace, tagRef.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("while deleting tag: %w", err)
	}
	return tag, nil
}

// releaseTagSnapshot deletes the objects the tag's external snapshot is made
// of. It tolerates a partly-collected snapshot, so a retry finishes cleanly.
// It collects the in-progress snapshot too.
func (s *RPCService) releaseTagSnapshot(ctx context.Context, tag *ateapipb.Tag) error {
	if s.objectStore == nil {
		return nil
	}
	atespace, name := tag.GetMetadata().GetAtespace(), tag.GetMetadata().GetName()
	for _, snapshotURI := range []string{
		tag.GetStatus().GetSnapshot().GetSnapshotUri(),
		tag.GetStatus().GetInProgressSnapshotUri(),
	} {
		if snapshotURI == "" {
			continue
		}
		uri, err := resources.ParseSnapshotURI(snapshotURI)
		if err != nil {
			return fmt.Errorf("while parsing the external snapshot %q of tag %s/%s: %w", snapshotURI, atespace, name, err)
		}
		if err := objectstore.DeletePrefix(ctx, s.objectStore, uri.Prefix()); err != nil {
			return fmt.Errorf("while releasing the external snapshot %q of tag %s/%s: %w", snapshotURI, atespace, name, err)
		}
	}
	return nil
}

func (s *ServiceImpl) DeleteTag(ctx context.Context, tagRef resources.TagRef) (*ateapipb.Tag, error) {
	// TODO: implement this
	return s.store.DeleteTag(ctx, tagRef)
}
