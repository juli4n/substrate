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

	"go.ate.dev/substrate/internal/proto/workerservicepb"
	"go.ate.dev/substrate/internal/resources"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ateDeepEqual is the deep-equal function declarative validation's generated
// code calls by name; it delegates to resources.DeepEqual.
func ateDeepEqual[T any](a, b T) bool {
	return resources.DeepEqual(a, b)
}

func validateRegisterWorkerRequest(ctx context.Context, req *workerservicepb.RegisterWorkerRequest) field.ErrorList {
	return Validate_RegisterWorkerRequest(ctx, operation.Operation{Type: operation.Create}, nil, req, nil)
}

func validateMintWorkerActorCertificateRequest(ctx context.Context, req *workerservicepb.MintWorkerActorCertificateRequest) field.ErrorList {
	return Validate_MintWorkerActorCertificateRequest(ctx, operation.Operation{Type: operation.Create}, nil, req, nil)
}

func validateMintWorkerActorJwtRequest(ctx context.Context, req *workerservicepb.MintWorkerActorJwtRequest) field.ErrorList {
	return Validate_MintWorkerActorJwtRequest(ctx, operation.Operation{Type: operation.Create}, nil, req, nil)
}

func validateRequestActorSuspendRequest(ctx context.Context, req *workerservicepb.RequestActorSuspendRequest) field.ErrorList {
	return Validate_RequestActorSuspendRequest(ctx, operation.Operation{Type: operation.Create}, nil, req, nil)
}

func validateReportActorCrashedRequest(ctx context.Context, req *workerservicepb.ReportActorCrashedRequest) field.ErrorList {
	return Validate_ReportActorCrashedRequest(ctx, operation.Operation{Type: operation.Create}, nil, req, nil)
}

func validateReportActorSnapshotStateRequest(ctx context.Context, req *workerservicepb.ReportActorSnapshotStateRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	errs := Validate_ReportActorSnapshotStateRequest(ctx, op, nil, req, nil)
	// A report with neither latest_local nor uploading is a detach, which
	// names the snapshot it made durable.
	if st := req.GetState(); st != nil && st.GetLatestLocal() == nil && st.GetUploading() == nil && st.GetLatestDurable() == nil {
		errs = append(errs, field.Required(field.NewPath("state", "latest_durable"), "a report with neither latest_local nor uploading must set latest_durable"))
	}
	return errs
}

func ValidateCustom_SandboxRuntime_SandboxClass(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	return resources.ValidateSandboxClass(fldPath, *value)
}
