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

package workerservice

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.ate.dev/substrate/internal/proto/workerservicepb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func assertValidateErr(t *testing.T, got field.ErrorList, want field.ErrorList) {
	t.Helper()
	field.ErrorMatcher{}.ByType().ByField().ByOrigin().Test(t, want, got)
}

func validSandboxRuntime() *workerservicepb.SandboxRuntime {
	return &workerservicepb.SandboxRuntime{
		SandboxClass: "gvisor",
		Version: &workerservicepb.VersionedSandboxCompat{
			SchemaVersion: "v1",
			Attributes:    []*workerservicepb.AttributeEntry{{Key: "architecture", Value: "amd64"}},
		},
	}
}

func attributeEntries(n int) []*workerservicepb.AttributeEntry {
	attrs := make([]*workerservicepb.AttributeEntry, n)
	for i := range attrs {
		attrs[i] = &workerservicepb.AttributeEntry{Key: fmt.Sprintf("k%d", i), Value: "v"}
	}
	return attrs
}

func sandboxRuntimes(n int) []*workerservicepb.SandboxRuntime {
	rts := make([]*workerservicepb.SandboxRuntime, n)
	for i := range rts {
		rts[i] = validSandboxRuntime()
	}
	return rts
}

func TestValidateRegisterWorkerRequest(t *testing.T) {
	validRuntime := validSandboxRuntime
	valid := func(mutate ...func(*workerservicepb.RegisterWorkerRequest)) *workerservicepb.RegisterWorkerRequest {
		r := &workerservicepb.RegisterWorkerRequest{
			Worker: &workerservicepb.ObjectRef{Name: testWorkerName},
			Capacity: &workerservicepb.WorkerResources{
				Actors:        10,
				CpuMillicores: 2000, MemoryBytes: 4 << 30,
			},
			DefaultRuntime: validRuntime(),
		}
		for _, m := range mutate {
			m(r)
		}
		return r
	}
	defaultRuntimePath := field.NewPath("default_runtime")
	restorablePath := field.NewPath("restorable_runtimes")
	tests := []struct {
		name string
		req  *workerservicepb.RegisterWorkerRequest
		want field.ErrorList
	}{{
		name: "valid",
		req:  valid(),
	}, {
		name: "valid with restorable runtimes",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.RestorableRuntimes = []*workerservicepb.SandboxRuntime{{
				SandboxClass: "gvisor",
				Version: &workerservicepb.VersionedSandboxCompat{
					SchemaVersion: "v1",
					Attributes: []*workerservicepb.AttributeEntry{
						{Key: "architecture", Value: "amd64"},
					},
				},
			}}
		}),
	}, {
		name: "valid empty capacity",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Capacity = &workerservicepb.WorkerResources{} }),
	}, {
		name: "missing worker",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Worker = nil }),
		want: field.ErrorList{field.Required(field.NewPath("worker"), "")},
	}, {
		name: "worker.atespace must be empty",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Worker.Atespace = "team-a" }),
		want: field.ErrorList{field.Forbidden(field.NewPath("worker", "atespace"), "")},
	}, {
		name: "missing worker.name",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Worker.Name = "" }),
		want: field.ErrorList{field.Required(field.NewPath("worker", "name"), "")},
	}, {
		name: "invalid worker.name",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Worker.Name = "UPPER" }),
		want: field.ErrorList{field.Invalid(field.NewPath("worker", "name"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "missing capacity",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Capacity = nil }),
		want: field.ErrorList{field.Required(field.NewPath("capacity"), "")},
	}, {
		name: "negative capacity.actors",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Capacity.Actors = -1 }),
		want: field.ErrorList{field.Invalid(field.NewPath("capacity", "actors"), nil, "").WithOrigin("minimum")},
	}, {
		name: "negative capacity.memory_bytes",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.Capacity.MemoryBytes = -1 }),
		want: field.ErrorList{field.Invalid(field.NewPath("capacity", "memory_bytes"), nil, "").WithOrigin("minimum")},
	}, {
		name: "missing default_runtime",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime = nil }),
		want: field.ErrorList{field.Required(defaultRuntimePath, "")},
	}, {
		name: "missing default_runtime.sandbox_class",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime.SandboxClass = "" }),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("sandbox_class"), "")},
	}, {
		name: "default_runtime.sandbox_class too long",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.SandboxClass = strings.Repeat("c", 64)
		}),
		want: field.ErrorList{
			field.TooLong(defaultRuntimePath.Child("sandbox_class"), "", 63).WithOrigin("maxLength"),
			field.NotSupported[string](defaultRuntimePath.Child("sandbox_class"), nil, nil),
		},
	}, {
		name: "unsupported default_runtime.sandbox_class",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime.SandboxClass = "kata" }),
		want: field.ErrorList{field.NotSupported[string](defaultRuntimePath.Child("sandbox_class"), nil, nil)},
	}, {
		name: "missing default_runtime.version",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime.Version = nil }),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("version"), "")},
	}, {
		name: "missing default_runtime.version.schema_version",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime.Version.SchemaVersion = "" }),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("version", "schema_version"), "")},
	}, {
		name: "default_runtime.version.schema_version too long",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.SchemaVersion = strings.Repeat("v", 65)
		}),
		want: field.ErrorList{field.TooLong(defaultRuntimePath.Child("version", "schema_version"), "", 64).WithOrigin("maxLength")},
	}, {
		name: "missing default_runtime.version.attributes",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.DefaultRuntime.Version.Attributes = nil }),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("version", "attributes"), "")},
	}, {
		name: "duplicate attribute key",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = []*workerservicepb.AttributeEntry{
				{Key: "architecture", Value: "amd64"},
				{Key: "architecture", Value: "arm64"},
			}
		}),
		want: field.ErrorList{field.Duplicate(defaultRuntimePath.Child("version", "attributes").Index(1), nil)},
	}, {
		name: "missing attribute key",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = []*workerservicepb.AttributeEntry{{Value: "amd64"}}
		}),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("version", "attributes").Index(0).Child("key"), "")},
	}, {
		name: "attribute key too long",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = []*workerservicepb.AttributeEntry{{Key: strings.Repeat("k", 129), Value: "v"}}
		}),
		want: field.ErrorList{field.TooLong(defaultRuntimePath.Child("version", "attributes").Index(0).Child("key"), "", 128).WithOrigin("maxLength")},
	}, {
		name: "missing attribute value",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = []*workerservicepb.AttributeEntry{{Key: "k"}}
		}),
		want: field.ErrorList{field.Required(defaultRuntimePath.Child("version", "attributes").Index(0).Child("value"), "")},
	}, {
		name: "attribute value too long",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = []*workerservicepb.AttributeEntry{{Key: "k", Value: strings.Repeat("v", 257)}}
		}),
		want: field.ErrorList{field.TooLong(defaultRuntimePath.Child("version", "attributes").Index(0).Child("value"), "", 256).WithOrigin("maxLength")},
	}, {
		name: "attributes at the bound",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = attributeEntries(32)
		}),
	}, {
		name: "too many attributes",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.DefaultRuntime.Version.Attributes = attributeEntries(33)
		}),
		want: field.ErrorList{field.TooMany(defaultRuntimePath.Child("version", "attributes"), 33, 32).WithOrigin("maxItems")},
	}, {
		name: "nil restorable_runtimes entry",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.RestorableRuntimes = []*workerservicepb.SandboxRuntime{nil}
		}),
		want: field.ErrorList{field.Required(restorablePath.Index(0), "")},
	}, {
		name: "invalid restorable_runtimes entry",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.RestorableRuntimes = []*workerservicepb.SandboxRuntime{{SandboxClass: "gvisor"}}
		}),
		want: field.ErrorList{field.Required(restorablePath.Index(0).Child("version"), "")},
	}, {
		name: "unsupported restorable_runtimes sandbox_class",
		req: valid(func(r *workerservicepb.RegisterWorkerRequest) {
			r.RestorableRuntimes = []*workerservicepb.SandboxRuntime{validSandboxRuntime()}
			r.RestorableRuntimes[0].SandboxClass = "GVISOR"
		}),
		want: field.ErrorList{field.NotSupported[string](restorablePath.Index(0).Child("sandbox_class"), nil, nil)},
	}, {
		name: "restorable_runtimes at the bound",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.RestorableRuntimes = sandboxRuntimes(32) }),
	}, {
		name: "too many restorable_runtimes",
		req:  valid(func(r *workerservicepb.RegisterWorkerRequest) { r.RestorableRuntimes = sandboxRuntimes(33) }),
		want: field.ErrorList{field.TooMany(restorablePath, 33, 32).WithOrigin("maxItems")},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidateErr(t, validateRegisterWorkerRequest(context.Background(), tt.req), tt.want)
		})
	}
}

func TestValidateRequestActorSuspendRequest(t *testing.T) {
	valid := func(mutate ...func(*workerservicepb.RequestActorSuspendRequest)) *workerservicepb.RequestActorSuspendRequest {
		r := &workerservicepb.RequestActorSuspendRequest{
			Worker:   &workerservicepb.ObjectRef{Name: testWorkerName},
			Actor:    &workerservicepb.ObjectRef{Atespace: "team-a", Name: "actor-1"},
			ActorUid: reportActorUID,
		}
		for _, m := range mutate {
			m(r)
		}
		return r
	}
	tests := []struct {
		name string
		req  *workerservicepb.RequestActorSuspendRequest
		want field.ErrorList
	}{{
		name: "valid",
		req:  valid(),
	}, {
		name: "missing worker",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Worker = nil }),
		want: field.ErrorList{field.Required(field.NewPath("worker"), "")},
	}, {
		name: "worker.atespace must be empty",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Worker.Atespace = "team-a" }),
		want: field.ErrorList{field.Forbidden(field.NewPath("worker", "atespace"), "")},
	}, {
		name: "missing worker.name",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Worker.Name = "" }),
		want: field.ErrorList{field.Required(field.NewPath("worker", "name"), "")},
	}, {
		name: "invalid worker.name",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Worker.Name = "UPPER" }),
		want: field.ErrorList{field.Invalid(field.NewPath("worker", "name"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "missing actor",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Actor = nil }),
		want: field.ErrorList{field.Required(field.NewPath("actor"), "")},
	}, {
		name: "missing actor.atespace",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Actor.Atespace = "" }),
		want: field.ErrorList{field.Required(field.NewPath("actor", "atespace"), "")},
	}, {
		name: "invalid actor.atespace",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Actor.Atespace = "../escape" }),
		want: field.ErrorList{field.Invalid(field.NewPath("actor", "atespace"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "missing actor.name",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Actor.Name = "" }),
		want: field.ErrorList{field.Required(field.NewPath("actor", "name"), "")},
	}, {
		name: "invalid actor.name",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.Actor.Name = "UPPER" }),
		want: field.ErrorList{field.Invalid(field.NewPath("actor", "name"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "missing actor_uid",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.ActorUid = "" }),
		want: field.ErrorList{field.Required(field.NewPath("actor_uid"), "")},
	}, {
		name: "invalid actor_uid",
		req:  valid(func(r *workerservicepb.RequestActorSuspendRequest) { r.ActorUid = "not-a-uuid" }),
		want: field.ErrorList{field.Invalid(field.NewPath("actor_uid"), nil, "").WithOrigin("format=k8s-uuid")},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidateErr(t, validateRequestActorSuspendRequest(context.Background(), tt.req), tt.want)
		})
	}
}

func TestValidateReportActorSnapshotStateRequest(t *testing.T) {
	report := func(gen int32) *workerservicepb.SnapshotReport {
		return &workerservicepb.SnapshotReport{
			Uuid:            fmt.Sprintf("snap-%d", gen),
			Generation:      gen,
			Fidelity:        workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY,
			Object:          &workerservicepb.ObjectSnapshot{SnapshotUri: fmt.Sprintf("gs://bucket/actors/a/snap-%d", gen)},
			LocalCommitTime: timestamppb.Now(),
		}
	}
	valid := func(mutate ...func(*workerservicepb.ReportActorSnapshotStateRequest)) *workerservicepb.ReportActorSnapshotStateRequest {
		durable := report(1)
		durable.DurableCompleteTime = timestamppb.Now()
		r := &workerservicepb.ReportActorSnapshotStateRequest{
			Actor:    &workerservicepb.ObjectRef{Atespace: "team-a", Name: "actor-1"},
			ActorUid: reportActorUID,
			State: &workerservicepb.ActorSnapshotState{
				LatestLocal:   report(2),
				Uploading:     report(2),
				LatestDurable: durable,
				UploadDueTime: timestamppb.Now(),
			},
		}
		for _, m := range mutate {
			m(r)
		}
		return r
	}
	local := field.NewPath("state", "latest_local")
	tests := []struct {
		name string
		req  *workerservicepb.ReportActorSnapshotStateRequest
		want field.ErrorList
	}{{
		name: "valid",
		req:  valid(),
	}, {
		name: "valid detach: no local snapshot",
		req: valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) {
			r.State.LatestLocal = nil
			r.State.Uploading = nil
			r.State.UploadDueTime = nil
		}),
	}, {
		name: "empty state",
		req: valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) {
			r.State = &workerservicepb.ActorSnapshotState{}
		}),
		want: field.ErrorList{field.Required(field.NewPath("state", "latest_durable"), "")},
	}, {
		name: "valid upload in flight with no local snapshot",
		req: valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) {
			r.State = &workerservicepb.ActorSnapshotState{Uploading: r.State.GetUploading()}
		}),
	}, {
		name: "missing actor",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.Actor = nil }),
		want: field.ErrorList{field.Required(field.NewPath("actor"), "")},
	}, {
		name: "missing actor.atespace",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.Actor.Atespace = "" }),
		want: field.ErrorList{field.Required(field.NewPath("actor", "atespace"), "")},
	}, {
		name: "invalid actor.name",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.Actor.Name = "UPPER" }),
		want: field.ErrorList{field.Invalid(field.NewPath("actor", "name"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "missing actor_uid",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.ActorUid = "" }),
		want: field.ErrorList{field.Required(field.NewPath("actor_uid"), "")},
	}, {
		name: "invalid actor_uid",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.ActorUid = "not-a-uuid" }),
		want: field.ErrorList{field.Invalid(field.NewPath("actor_uid"), nil, "").WithOrigin("format=k8s-uuid")},
	}, {
		name: "missing state",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State = nil }),
		want: field.ErrorList{field.Required(field.NewPath("state"), "")},
	}, {
		name: "missing uuid",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Uuid = "" }),
		want: field.ErrorList{field.Required(local.Child("uuid"), "")},
	}, {
		name: "invalid uuid",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Uuid = "../escape" }),
		want: field.ErrorList{field.Invalid(local.Child("uuid"), nil, "").WithOrigin("format=k8s-short-name")},
	}, {
		name: "generation below 1",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Generation = -1 }),
		want: field.ErrorList{field.Invalid(local.Child("generation"), nil, "").WithOrigin("minimum")},
	}, {
		name: "missing fidelity",
		req: valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) {
			r.State.LatestLocal.Fidelity = workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_UNSPECIFIED
		}),
		want: field.ErrorList{field.Required(local.Child("fidelity"), "")},
	}, {
		name: "unknown fidelity",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Fidelity = 4 }),
		want: field.ErrorList{field.Invalid(local.Child("fidelity"), nil, "").WithOrigin("maximum")},
	}, {
		name: "missing object",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Object = nil }),
		want: field.ErrorList{field.Required(local.Child("object"), "")},
	}, {
		name: "missing object.snapshot_uri",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestLocal.Object.SnapshotUri = "" }),
		want: field.ErrorList{field.Required(local.Child("object", "snapshot_uri"), "")},
	}, {
		name: "valid without local_commit_time",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestDurable.LocalCommitTime = nil }),
	}, {
		name: "uploading is validated",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.Uploading.Uuid = "" }),
		want: field.ErrorList{field.Required(field.NewPath("state", "uploading", "uuid"), "")},
	}, {
		name: "latest_durable is validated",
		req:  valid(func(r *workerservicepb.ReportActorSnapshotStateRequest) { r.State.LatestDurable.Object = nil }),
		want: field.ErrorList{field.Required(field.NewPath("state", "latest_durable", "object"), "")},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidateErr(t, validateReportActorSnapshotStateRequest(context.Background(), tt.req), tt.want)
		})
	}
}
