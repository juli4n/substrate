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

package validation

import (
	"context"
	"slices"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/validate"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateCreateActorRequest(ctx context.Context, req *ateapipb.CreateActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_CreateActorRequest(ctx, op, nil, req, nil)
}

func ValidateGetActorRequest(ctx context.Context, req *ateapipb.GetActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_GetActorRequest(ctx, op, nil, req, nil)
}

func ValidateListActorsRequest(ctx context.Context, req *ateapipb.ListActorsRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_ListActorsRequest(ctx, op, nil, req, nil)
}

func ValidateUpdateActorRequest(ctx context.Context, req *ateapipb.UpdateActorRequest) field.ErrorList {
	// We model this as a create rather than an update because updates assume
	// the existence of a "current" value, which we do not have yet.  This is
	// validating the request itself. The result will be validated later, after
	// we have a current value to compare against.
	op := operation.Operation{Type: operation.Create}
	return Validate_UpdateActorRequest(ctx, op, nil, req, nil)
}

func ValidateDeleteActorRequest(ctx context.Context, req *ateapipb.DeleteActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_DeleteActorRequest(ctx, op, nil, req, nil)
}

func ValidatePauseActorRequest(ctx context.Context, req *ateapipb.PauseActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_PauseActorRequest(ctx, op, nil, req, nil)
}

func ValidateResumeActorRequest(ctx context.Context, req *ateapipb.ResumeActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_ResumeActorRequest(ctx, op, nil, req, nil)
}

func ValidateSuspendActorRequest(ctx context.Context, req *ateapipb.SuspendActorRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_SuspendActorRequest(ctx, op, nil, req, nil)
}

func ValidateActorUpdate(ctx context.Context, fldPath *field.Path, newVal, oldVal *ateapipb.Actor, requireStatus bool) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	errs := Validate_Actor(ctx, op, fldPath, newVal, oldVal)
	if requireStatus {
		// Status is optional in the schema, but is actually required to be set
		// by the server.  If it was specified, it was already validated above,
		// but if it was not specified we need to flag that as an error.
		errs = append(errs, validate.RequiredPointer(ctx, op, fldPath.Child("status"), newVal.GetStatus(), nil)...)
	}
	return errs
}

// ValidateTemplateVolumesUnchanged rejects a template repoint that changes
// the template's volumes or any container's volume mounts: an actor's
// snapshot data is laid out per the volumes and mount paths it was captured
// with, so a different layout would restore it to the wrong places. The
// volumes list must be identical, and containers present in both templates
// must keep identical mounts, order included; containers added or removed by
// the new template are unconstrained.
func ValidateTemplateVolumesUnchanged(oldTemplate, newTemplate *ateapipb.ActorTemplate) error {
	if !slices.EqualFunc(oldTemplate.GetVolumes(), newTemplate.GetVolumes(), func(a, b *ateapipb.Volume) bool {
		return proto.Equal(a, b)
	}) {
		return status.Error(codes.FailedPrecondition,
			"volumes differ between the current and the new actor template; volumes must be identical to repoint an actor")
	}

	newContainers := make(map[string]*ateapipb.Container, len(newTemplate.GetContainers()))
	for _, c := range newTemplate.GetContainers() {
		newContainers[c.GetName()] = c
	}
	for _, oldC := range oldTemplate.GetContainers() {
		newC, ok := newContainers[oldC.GetName()]
		if !ok {
			continue
		}
		if !slices.EqualFunc(oldC.GetVolumeMounts(), newC.GetVolumeMounts(), func(a, b *ateapipb.VolumeMount) bool {
			return proto.Equal(a, b)
		}) {
			return status.Errorf(codes.FailedPrecondition,
				"volume mounts of container %q differ between the current and the new actor template; volume mounts must be identical to repoint an actor", oldC.GetName())
		}
	}
	return nil
}

// This exists only because nested subfield tags are not supported yet.
func ValidateCustom_UpdateActorRequest_Actor(ctx context.Context, op operation.Operation, fldPath *field.Path, actor, _ *ateapipb.Actor) field.ErrorList {
	if actor == nil || actor.Metadata == nil {
		return nil // handled by DV
	}

	// Updates are validated in 2 steps: first the update request and then the
	// resource itself. DV for the request doesn't descend into the resource
	// metadata.  Once DV supports nested subfield tags, this can be changed to
	// something like:
	//   +k8s:subfield(metadata)=+k8s:subfield(atespace)=+k8s:required
	errs := Validate_ResourceMetadata(ctx, op, fldPath.Child("metadata"), actor.Metadata, nil)
	errs = append(errs, validate.RequiredValue(ctx, op, fldPath.Child("metadata", "atespace"), &actor.Metadata.Atespace, nil)...)
	return errs
}
