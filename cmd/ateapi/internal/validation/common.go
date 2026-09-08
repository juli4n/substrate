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
	"strings"

	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// This is needed because DV doesn't have a standard format for IP addresses yet.
func ValidateCustom_WorkerAssignment_WorkerPodIp(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	return validation.IsValidIP(fldPath, *value)
}

// ValidateCustom_ExternalVolume_VolumeType checks that a volume type string is well-formed.
// It allows an optional "substrate.io/" prefix, followed by a valid DNS-1123 subdomain.
func ValidateCustom_ExternalVolume_VolumeType(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil || *value == "" {
		return nil
	}
	var errs field.ErrorList
	valToValidate := strings.TrimPrefix(*value, "substrate.io/")
	for _, msg := range validation.IsDNS1123Subdomain(valToValidate) {
		errs = append(errs, field.Invalid(fldPath, *value, msg))
	}
	return errs
}

// ValidateCustom_ExternalVolume_StorageVolumeId checks that an external volume's storage ID does not
// contain control characters (U+0000-U+0008, U+000B, U+000C, U+000E-U+001F, U+007F-U+009F).
func ValidateCustom_ExternalVolume_StorageVolumeId(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if value == nil || *value == "" {
		return nil
	}
	for _, r := range *value {
		if (r >= 0x0000 && r <= 0x0008) ||
			r == 0x000B ||
			r == 0x000C ||
			(r >= 0x000E && r <= 0x001F) ||
			(r >= 0x007F && r <= 0x009F) {
			return field.ErrorList{field.Invalid(fldPath, *value, "must not contain control characters (U+0000-U+0008, U+000B, U+000C, U+000E-U+001F, U+007F-U+009F)")}
		}
	}
	return nil
}
