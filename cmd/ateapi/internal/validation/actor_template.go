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
	"fmt"
	"regexp"
	"strings"

	"github.com/agent-substrate/substrate/internal/volumepath"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"k8s.io/apimachinery/pkg/api/operation"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateCreateActorTemplateRequest(ctx context.Context, req *ateapipb.CreateActorTemplateRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_CreateActorTemplateRequest(ctx, op, nil, req, nil)
}

func ValidateActorTemplateUpdate(ctx context.Context, fldPath *field.Path, newVal, oldVal *ateapipb.ActorTemplate) field.ErrorList {
	op := operation.Operation{Type: operation.Update}
	return Validate_ActorTemplate(ctx, op, fldPath, newVal, oldVal)
}

func ValidateGetActorTemplateRequest(ctx context.Context, req *ateapipb.GetActorTemplateRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_GetActorTemplateRequest(ctx, op, nil, req, nil)
}

func ValidateListActorTemplatesRequest(ctx context.Context, req *ateapipb.ListActorTemplatesRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_ListActorTemplatesRequest(ctx, op, nil, req, nil)
}

func ValidateDeleteActorTemplateRequest(ctx context.Context, req *ateapipb.DeleteActorTemplateRequest) field.ErrorList {
	op := operation.Operation{Type: operation.Create}
	return Validate_DeleteActorTemplateRequest(ctx, op, nil, req, nil)
}

// httpGetPathRE constrains readyz paths to RFC 3986 path-segment
// characters only, with well-formed percent-escapes, and no query string
// or fragment.
var httpGetPathRE = regexp.MustCompile(`^/([A-Za-z0-9\-._~!$&'()*+,;=:@/]|%[0-9A-Fa-f]{2})*$`)

func ValidateCustom_HTTPGetAction_Path(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if !httpGetPathRE.MatchString(*value) {
		return field.ErrorList{field.Invalid(fldPath, *value, "must be a URL path starting with '/', using only RFC 3986 path-segment characters, without query or fragment")}
	}
	return nil
}

// mountPathBadSegmentRE matches '.' or '..' path segments.
var mountPathBadSegmentRE = regexp.MustCompile(`(^|/)[.][.]?(/|$)`)

// ValidateCustom_VolumeMount_MountPath requires a clean absolute Unix path
// that starts with '/', is not '/', and contains no ':', '.' or '..'
// segments, '//', trailing '/', or control characters.
func ValidateCustom_VolumeMount_MountPath(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	p := *value
	bad := !strings.HasPrefix(p, "/") || len(p) == 1 ||
		strings.HasSuffix(p, "/") || strings.Contains(p, "//") ||
		strings.Contains(p, ":") || mountPathBadSegmentRE.MatchString(p)
	if !bad {
		for _, r := range p {
			if r < 0x20 || r == 0x7f {
				bad = true
				break
			}
		}
	}
	if bad {
		return field.ErrorList{field.Invalid(fldPath, p, "must be a clean absolute Unix path: must start with '/', not be '/', and contain no ':', '..', '.', '//', trailing '/', or control characters")}
	}
	return nil
}

// validateProjectedPath applies the projected-path rule shared with atelet,
// which re-checks it before writing to the host.
func validateProjectedPath(fldPath *field.Path, p string) field.ErrorList {
	if err := volumepath.ValidateProjected(p); err != nil {
		return field.ErrorList{field.Invalid(fldPath, p, err.Error())}
	}
	return nil
}

func ValidateCustom_ActorMetadataItem_Path(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	return validateProjectedPath(fldPath, *value)
}

func ValidateCustom_TrustBundleDataSource_Path(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	return validateProjectedPath(fldPath, *value)
}

// ValidateCustom_SystemInfoVolumeSource_DataSources requires every projected
// file path to be unique across all data sources: atelet writes them in
// order into one tree, so a repeated path silently clobbers the earlier file.
func ValidateCustom_SystemInfoVolumeSource_DataSources(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []*ateapipb.SystemInfoDataSource) field.ErrorList {
	var errs field.ErrorList
	seen := sets.New[string]()
	for i, ds := range value {
		switch {
		case ds == nil:
		case ds.TrustBundle != nil:
			if seen.Has(ds.TrustBundle.Path) {
				errs = append(errs, field.Duplicate(fldPath.Index(i).Child("trust_bundle", "path"), ds.TrustBundle.Path))
			}
			seen.Insert(ds.TrustBundle.Path)
		case ds.ActorMetadata != nil:
			for j, item := range ds.ActorMetadata.Items {
				if item == nil {
					continue
				}
				if seen.Has(item.Path) {
					errs = append(errs, field.Duplicate(fldPath.Index(i).Child("actor_metadata", "items").Index(j).Child("path"), item.Path))
				}
				seen.Insert(item.Path)
			}
		}
	}
	return errs
}

// ValidateCustom_ImageVolumeSource_Reference requires image references to
// be pinned by digest, because changing the image content under a fixed
// reference invalidates snapshots.
func ValidateCustom_ImageVolumeSource_Reference(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if !strings.Contains(*value, "@") {
		return field.ErrorList{field.Invalid(fldPath, *value, "must be pinned by digest (changing the image invalidates snapshots)")}
	}
	return nil
}

func ValidateCustom_ExternalVolumeTemplate_Capacity(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if _, err := resource.ParseQuantity(*value); err != nil {
		return field.ErrorList{field.Invalid(fldPath, *value, fmt.Sprintf("must be a Kubernetes resource quantity: %v", err))}
	}
	return nil
}

// cpuLimitMax bounds cpu limits: they must be less than 1000 cores.
var cpuLimitMax = resource.MustParse("1k")

// ValidateCustom_Resources_Limits validates the resource limits: only cpu
// and memory limits are supported, each quantity must be greater than zero,
// and the cpu limit must be less than 1000 cores. Presence and uniqueness
// of names are enforced by tags.
func ValidateCustom_Resources_Limits(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []*ateapipb.Limits) field.ErrorList {
	var errs field.ErrorList
	for i, limit := range value {
		if limit == nil {
			continue
		}
		if limit.Name != "cpu" && limit.Name != "memory" {
			errs = append(errs, field.NotSupported(fldPath.Index(i).Child("name"), limit.Name, []string{"cpu", "memory"}))
			continue
		}
		if limit.Quantity == "" {
			continue // required is enforced by tags
		}
		q, err := resource.ParseQuantity(limit.Quantity)
		if err != nil {
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("quantity"), limit.Quantity, fmt.Sprintf("must be a Kubernetes resource quantity: %v", err)))
			continue
		}
		if q.Sign() <= 0 {
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("quantity"), limit.Quantity, "must be greater than zero"))
		}
		if limit.Name == "cpu" && q.Cmp(cpuLimitMax) >= 0 {
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("quantity"), limit.Quantity, "cpu limit must be less than 1000 cores"))
		}
	}
	return errs
}

// ValidateCustom_SnapshotsConfig requires on_commit to be a
// subset of on_pause. UNSPECIFIED means FULL, so an unset on_commit over a
// DATA on_pause is rejected too.
func ValidateCustom_SnapshotsConfig(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *ateapipb.SnapshotsConfig) field.ErrorList {
	if value.GetOnPause() == ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA &&
		value.GetOnCommit() != ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA {
		return field.ErrorList{field.Invalid(fldPath.Child("on_commit"), value.GetOnCommit().String(), "must be a subset of on_pause")}
	}
	return nil
}

// envVarNameRE constrains env var names to any printable ASCII character
// except '='.
var envVarNameRE = regexp.MustCompile(`^[ -<>-~]+$`)

func ValidateCustom_EnvVar_Name(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *string) field.ErrorList {
	if !envVarNameRE.MatchString(*value) {
		return field.ErrorList{field.Invalid(fldPath, *value, "may contain any printable ASCII character except '='")}
	}
	return nil
}

// capabilityRE constrains Linux capability names: uppercase, without the
// "CAP_" prefix (which is added when the OCI spec is written; the prefixed
// spelling would silently grant nothing).
var capabilityRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func validateCapabilities(fldPath *field.Path, caps []string, allowAll bool) field.ErrorList {
	var errs field.ErrorList
	for i, c := range caps {
		p := fldPath.Index(i)
		switch {
		case c == "ALL" && !allowAll:
			errs = append(errs, field.Invalid(p, c, "add does not accept 'ALL'; name the individual capabilities the container needs"))
		case c == "ALL":
		case len(c) > 63:
			errs = append(errs, field.TooLong(p, nil, 63))
		case strings.HasPrefix(c, "CAP_"):
			errs = append(errs, field.Invalid(p, c, "must be named without the 'CAP_' prefix (e.g. 'NET_BIND_SERVICE')"))
		case !capabilityRE.MatchString(c):
			errs = append(errs, field.Invalid(p, c, "must be an uppercase capability name like 'NET_BIND_SERVICE'"))
		}
	}
	return errs
}

func ValidateCustom_Capabilities_Add(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []string) field.ErrorList {
	return validateCapabilities(fldPath, value, false)
}

func ValidateCustom_Capabilities_Drop(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []string) field.ErrorList {
	return validateCapabilities(fldPath, value, true)
}

// ValidateCustom_Container_VolumeMounts rejects two mounts at the same path
// within one container. The list is keyed by volume name (one mount per
// volume), so path uniqueness cannot come from the list-map key
func ValidateCustom_Container_VolumeMounts(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ []*ateapipb.VolumeMount) field.ErrorList {
	var errs field.ErrorList
	seen := make(map[string]bool, len(value))
	for i, m := range value {
		path := m.GetMountPath()
		if path == "" {
			continue // required is enforced by tags
		}
		if seen[path] {
			errs = append(errs, field.Duplicate(fldPath.Index(i).Child("mount_path"), path))
		}
		// Nested mounts are unsupported (volumes cannot mount onto
		// other volumes).
		for j := 0; j < i; j++ {
			prior := value[j].GetMountPath()
			if prior == "" || prior == path {
				continue
			}
			if strings.HasPrefix(path, prior+"/") || strings.HasPrefix(prior, path+"/") {
				errs = append(errs, field.Invalid(fldPath.Index(i).Child("mount_path"), path,
					fmt.Sprintf("must not nest under or over another mount (%q)", prior)))
			}
		}
		seen[path] = true
	}
	return errs
}

// ValidateCustom_CreateActorTemplateRequest_ActorTemplate rejects container
// volume mounts that reference volumes the template does not declare.
func ValidateCustom_CreateActorTemplateRequest_ActorTemplate(_ context.Context, _ operation.Operation, fldPath *field.Path, value, _ *ateapipb.ActorTemplate) field.ErrorList {
	declared := make(map[string]bool, len(value.GetVolumes()))
	for _, vol := range value.GetVolumes() {
		declared[vol.GetName()] = true
	}
	var errs field.ErrorList
	for i, ctr := range value.GetContainers() {
		for j, mount := range ctr.GetVolumeMounts() {
			name := mount.GetName()
			if name == "" {
				continue // required is enforced by tags
			}
			if !declared[name] {
				errs = append(errs, field.Invalid(
					fldPath.Child("containers").Index(i).Child("volume_mounts").Index(j).Child("name"),
					name, "must reference a volume declared in the template"))
			}
		}
	}
	return errs
}
