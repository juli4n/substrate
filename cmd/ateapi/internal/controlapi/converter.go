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
	"go.ate.dev/substrate/internal/proto/ateletpb"
	"go.ate.dev/substrate/internal/proto/workerservicepb"
	"go.ate.dev/substrate/pkg/proto/ateapipb"
)

// fidelityToAtelet maps the public fidelity ladder onto the identical atelet
// wire enum, level by level. Anything else, including UNSPECIFIED, goes out
// as UNSPECIFIED, which atelet rejects, rather than being silently promoted.
func fidelityToAtelet(in ateapipb.SnapshotFidelity) ateletpb.SnapshotFidelity {
	switch in {
	case ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES:
		return ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES
	case ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS:
		return ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS
	case ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY:
		return ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY
	default:
		return ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_UNSPECIFIED
	}
}

// fidelityFromAtelet maps atelet's fidelity enum onto the identical
// WorkerService one. Anything else comes back UNSPECIFIED.
func fidelityFromAtelet(in ateletpb.SnapshotFidelity) workerservicepb.SnapshotFidelity {
	switch in {
	case ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES:
		return workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES
	case ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS:
		return workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS
	case ateletpb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY:
		return workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY
	default:
		return workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_UNSPECIFIED
	}
}

// fidelityFromWorkerService maps WorkerService's fidelity enum onto the
// identical public one. Anything else comes back UNSPECIFIED.
func fidelityFromWorkerService(in workerservicepb.SnapshotFidelity) ateapipb.SnapshotFidelity {
	switch in {
	case workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES:
		return ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_VOLUMES
	case workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS:
		return ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_ROOTFS
	case workerservicepb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY:
		return ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_MEMORY
	default:
		return ateapipb.SnapshotFidelity_SNAPSHOT_FIDELITY_UNSPECIFIED
	}
}

// snapshotReportFromAtelet converts a snapshot atelet reports into the form
// ReportActorSnapshotState takes, which carries the snapshot's fields inline.
// A report that names no snapshot converts to nil.
func snapshotReportFromAtelet(r *ateletpb.SnapshotReport) *workerservicepb.SnapshotReport {
	if r.GetSnapshot() == nil {
		return nil
	}
	snap := r.GetSnapshot()
	out := &workerservicepb.SnapshotReport{
		Uuid:                snap.GetSnapshotUuid(),
		Generation:          snap.GetGeneration(),
		Fidelity:            fidelityFromAtelet(snap.GetFidelity()),
		LocalCommitTime:     r.GetLocalCommitTime(),
		DurableCompleteTime: r.GetDurableCompleteTime(),
	}
	if obj := snap.GetObject(); obj != nil {
		out.Object = &workerservicepb.ObjectSnapshot{SnapshotUri: obj.GetSnapshotUri()}
	}
	return out
}
