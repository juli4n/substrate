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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"

	"go.ate.dev/substrate/cmd/ateapi/internal/ateletauth/ateletauthtest"
	"go.ate.dev/substrate/cmd/ateapi/internal/store/storetest"
	"go.ate.dev/substrate/internal/apierror"
	"go.ate.dev/substrate/internal/proto/workerservicepb"
	"go.ate.dev/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
)

// testRuntime is a SandboxRuntime of the given class whose compatibility
// identity is a single architecture attribute, which is enough to tell two
// runtimes apart.
func testRuntime(class, arch string) *workerservicepb.SandboxRuntime {
	return &workerservicepb.SandboxRuntime{
		SandboxClass: class,
		Version: &workerservicepb.VersionedSandboxCompat{
			SchemaVersion: "v1",
			Attributes:    []*workerservicepb.AttributeEntry{{Key: "architecture", Value: arch}},
		},
	}
}

// testDefaultRuntime is what seedReportedWorker records as already reported,
// of the class it gives the Worker.
var testDefaultRuntime = testRuntime("gvisor", "amd64")

func setRequest(actors int32) *workerservicepb.RegisterWorkerRequest {
	return &workerservicepb.RegisterWorkerRequest{
		Worker:         &workerservicepb.ObjectRef{Name: testWorkerName},
		Capacity:       &workerservicepb.WorkerResources{Actors: actors},
		DefaultRuntime: testDefaultRuntime,
	}
}

// The point of the whole path: a Worker moves from what it reported before to
// what its worker reports now.
func TestRegisterWorker(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 1, CpuMillicores: 2000, MemoryBytes: 0})

	if _, err := s.RegisterWorker(ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode)), setRequest(4094)); err != nil {
		t.Fatalf("RegisterWorker() failed: %v", err)
	}
	after, err := st.GetWorker(context.Background(), testWorkerName)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if want := int32(4094); after.GetStatus().GetCapacity().GetActors() != want {
		t.Errorf("capacity.actors = %d, want %d", after.GetStatus().GetCapacity().GetActors(), want)
	}
	// A report replaces what is recorded. The Worker reports everything it has,
	// so a dimension this one leaves out is one it no longer supplies -- keeping
	// the old value would advertise compute nothing claims to have.
	if c := after.GetStatus().GetCapacity(); c.GetCpuMillicores() != 0 || c.GetMemoryBytes() != 0 {
		t.Errorf("capacity = %v, want the report's own (no cpu or memory)", c)
	}
}

// A report's runtimes are checked against the Worker's class and recorded only
// when they differ from what is stored. Capacity is held at what was seeded,
// so a write here is one the runtimes alone caused.
func TestRegisterWorker_Runtimes(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	authed := ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode))

	gvisorAMD64, gvisorARM64 := testRuntime("gvisor", "amd64"), testRuntime("gvisor", "arm64")
	gvisorV8, gvisorV9 := testRuntime("gvisor", "arm64-v8"), testRuntime("gvisor", "arm64-v9")
	microvm := testRuntime("microvm", "amd64")
	// Every Worker here has already reported gvisorAMD64 with gvisorV8 and
	// gvisorV9 restorable, in that order, at a capacity of one actor.
	seeded := func() *ateapipb.WorkerStatus {
		return &ateapipb.WorkerStatus{
			State:              ateapipb.WorkerState_WORKER_STATE_ACTIVE,
			Capacity:           &ateapipb.WorkerResources{Actors: 1},
			DefaultRuntime:     toAteAPISandboxRuntime(gvisorAMD64),
			RestorableRuntimes: toAteAPISandboxRuntimes([]*workerservicepb.SandboxRuntime{gvisorV8, gvisorV9}),
		}
	}

	tests := []struct {
		name string
		// workerClass is the class the pool created the Worker with; "" is
		// a Worker recorded without one.
		workerClass string
		def         *workerservicepb.SandboxRuntime
		restorable  []*workerservicepb.SandboxRuntime
		wantCode    codes.Code
		// wantWrite is whether the report changes the record.
		wantWrite bool
	}{{
		name:        "identical report writes nothing",
		workerClass: "gvisor",
		def:         gvisorAMD64,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV8, gvisorV9},
	}, {
		name:        "changed default runtime writes",
		workerClass: "gvisor",
		def:         gvisorARM64,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV8, gvisorV9},
		wantWrite:   true,
	}, {
		name:        "changed restorable runtime writes",
		workerClass: "gvisor",
		def:         gvisorAMD64,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV8, gvisorARM64},
		wantWrite:   true,
	}, {
		// A report is compared as sent: the list is atomic, so a new order
		// is a new report.
		name:        "reordered restorable runtimes write",
		workerClass: "gvisor",
		def:         gvisorAMD64,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV9, gvisorV8},
		wantWrite:   true,
	}, {
		// Runtimes are replaced like capacity: a report that lists no
		// restorable runtimes is a Worker that can restore from none but its
		// default, as when a version is disabled, and the record must not
		// keep advertising them.
		name:        "dropped restorable runtimes write",
		workerClass: "gvisor",
		def:         gvisorAMD64,
		wantWrite:   true,
	}, {
		// The class is fixed by the pool that created the Worker. A runtime
		// of another class is one it cannot run, so the report is refused
		// rather than recorded for the scheduler to act on.
		name:        "default runtime of another class is refused",
		workerClass: "gvisor",
		def:         microvm,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV8, gvisorV9},
		wantCode:    codes.FailedPrecondition,
	}, {
		name:        "restorable runtime of another class is refused",
		workerClass: "gvisor",
		def:         gvisorAMD64,
		restorable:  []*workerservicepb.SandboxRuntime{gvisorV8, microvm},
		wantCode:    codes.FailedPrecondition,
	}, {
		// A Worker recorded without a class has nothing for a report to
		// contradict.
		name:       "unclassed Worker takes a default runtime of any class",
		def:        microvm,
		restorable: []*workerservicepb.SandboxRuntime{gvisorV8, gvisorV9},
		wantWrite:  true,
	}, {
		name:       "unclassed Worker takes restorable runtimes of mixed classes",
		def:        gvisorAMD64,
		restorable: []*workerservicepb.SandboxRuntime{gvisorV8, microvm},
		wantWrite:  true,
	}}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := fmt.Sprintf("1a7e4c83-6d20-4f95-b3c8-%012d", i)
			before := seedWorker(t, st, name, testNode, tt.workerClass, seeded())

			_, err := s.RegisterWorker(authed, &workerservicepb.RegisterWorkerRequest{
				Worker:             &workerservicepb.ObjectRef{Name: name},
				Capacity:           &workerservicepb.WorkerResources{Actors: 1},
				DefaultRuntime:     tt.def,
				RestorableRuntimes: tt.restorable,
			})
			if code := apierror.Code(err); code != tt.wantCode {
				t.Fatalf("code = %v (err %v), want %v", code, err, tt.wantCode)
			}
			after, err := st.GetWorker(context.Background(), name)
			if err != nil {
				t.Fatalf("GetWorker: %v", err)
			}
			if wrote := after.GetMetadata().GetVersion() != before.GetMetadata().GetVersion(); wrote != tt.wantWrite {
				t.Fatalf("version %d -> %d, want a write: %v", before.GetMetadata().GetVersion(), after.GetMetadata().GetVersion(), tt.wantWrite)
			}
			if !tt.wantWrite {
				if diff := cmp.Diff(before.GetStatus(), after.GetStatus(), protocmp.Transform()); diff != "" {
					t.Errorf("status changed without a write (-want +got):\n%s", diff)
				}
				return
			}
			if diff := cmp.Diff(toAteAPISandboxRuntime(tt.def), after.GetStatus().GetDefaultRuntime(), protocmp.Transform()); diff != "" {
				t.Errorf("default_runtime mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(toAteAPISandboxRuntimes(tt.restorable), after.GetStatus().GetRestorableRuntimes(), protocmp.Transform(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("restorable_runtimes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// An atelet speaks for the Workers it herds and no others. A Worker on another
// node is reported as absent rather than forbidden, so a caller learns nothing
// about what runs elsewhere.
func TestRegisterWorker_OtherNodeIsNotFound(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 1})

	_, err := s.RegisterWorker(ateletauthtest.ContextWith(ateletauthtest.CertOn(t, "some-other-node")), setRequest(4094))
	if got := apierror.Code(err); got != codes.NotFound {
		t.Fatalf("code = %v (err %v), want NotFound", got, err)
	}

	// And the report must not have landed.
	after, err := st.GetWorker(context.Background(), testWorkerName)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got := after.GetStatus().GetCapacity().GetActors(); got != 1 {
		t.Errorf("capacity.actors = %d, want 1 unchanged", got)
	}
}

// Re-sending the same capacity is not an update. A worker reports once, but it
// retries until accepted and reports again if it restarts, so a repeat must not
// churn the Worker's version.
func TestRegisterWorker_UnchangedDoesNotWrite(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seeded := seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 4094})

	for range 3 {
		if _, err := s.RegisterWorker(ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode)), setRequest(4094)); err != nil {
			t.Fatalf("RegisterWorker() failed: %v", err)
		}
	}
	after, err := st.GetWorker(context.Background(), testWorkerName)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if got, want := after.GetMetadata().GetVersion(), seeded.GetMetadata().GetVersion(); got != want {
		t.Errorf("version = %d after three identical reports, want %d unchanged", got, want)
	}
}

// Attribute order is the producer's business, not the record's: the same
// attributes in another order are the same runtime, so they are stored sorted
// by key and a reordered repeat writes nothing.
func TestRegisterWorker_SortsAttributes(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 1})
	authed := ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode))

	runtimeWith := func(keys ...string) *workerservicepb.SandboxRuntime {
		rt := testRuntime("gvisor", "amd64")
		rt.Version.Attributes = nil
		for _, k := range keys {
			rt.Version.Attributes = append(rt.Version.Attributes, &workerservicepb.AttributeEntry{Key: k, Value: "v"})
		}
		return rt
	}
	register := func(def, restorable *workerservicepb.SandboxRuntime) *ateapipb.Worker {
		t.Helper()
		if _, err := s.RegisterWorker(authed, &workerservicepb.RegisterWorkerRequest{
			Worker:             &workerservicepb.ObjectRef{Name: testWorkerName},
			Capacity:           &workerservicepb.WorkerResources{Actors: 1},
			DefaultRuntime:     def,
			RestorableRuntimes: []*workerservicepb.SandboxRuntime{restorable},
		}); err != nil {
			t.Fatalf("RegisterWorker() failed: %v", err)
		}
		after, err := st.GetWorker(context.Background(), testWorkerName)
		if err != nil {
			t.Fatalf("GetWorker: %v", err)
		}
		return after
	}

	first := register(runtimeWith("cpu_features", "architecture"), runtimeWith("sandbox_bundle_hash", "architecture"))
	if diff := cmp.Diff(toAteAPISandboxRuntime(runtimeWith("architecture", "cpu_features")), first.GetStatus().GetDefaultRuntime(), protocmp.Transform()); diff != "" {
		t.Errorf("default_runtime not stored sorted by key (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]*ateapipb.SandboxRuntime{toAteAPISandboxRuntime(runtimeWith("architecture", "sandbox_bundle_hash"))}, first.GetStatus().GetRestorableRuntimes(), protocmp.Transform()); diff != "" {
		t.Errorf("restorable_runtimes not stored sorted by key (-want +got):\n%s", diff)
	}

	again := register(runtimeWith("architecture", "cpu_features"), runtimeWith("architecture", "sandbox_bundle_hash"))
	if got, want := again.GetMetadata().GetVersion(), first.GetMetadata().GetVersion(); got != want {
		t.Errorf("version = %d after a reordered repeat, want %d unchanged", got, want)
	}
}

func TestRegisterWorker_Errors(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 1})
	authed := ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode))

	tests := []struct {
		name string
		ctx  context.Context
		req  *workerservicepb.RegisterWorkerRequest
		want codes.Code
	}{
		{"unauthenticated", ateletauthtest.ContextWith(nil), setRequest(2), codes.Unauthenticated},
		{"no worker ref", authed, &workerservicepb.RegisterWorkerRequest{
			Capacity:       &workerservicepb.WorkerResources{Actors: 2},
			DefaultRuntime: testDefaultRuntime,
		}, codes.InvalidArgument},
		{"no capacity", authed, &workerservicepb.RegisterWorkerRequest{
			Worker:         &workerservicepb.ObjectRef{Name: testWorkerName},
			DefaultRuntime: testDefaultRuntime,
		}, codes.InvalidArgument},
		{"no default runtime", authed, &workerservicepb.RegisterWorkerRequest{
			Worker:   &workerservicepb.ObjectRef{Name: testWorkerName},
			Capacity: &workerservicepb.WorkerResources{Actors: 2},
		}, codes.InvalidArgument},
		{"absent worker", authed, &workerservicepb.RegisterWorkerRequest{
			Worker:         &workerservicepb.ObjectRef{Name: "3b9f1e77-2c4d-4a80-91be-6d5c8f0a7e21"},
			Capacity:       &workerservicepb.WorkerResources{Actors: 2},
			DefaultRuntime: testDefaultRuntime,
		}, codes.NotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.RegisterWorker(tc.ctx, tc.req)
			if got := apierror.Code(err); got != tc.want {
				t.Errorf("code = %v (err %v), want %v", got, err, tc.want)
			}
		})
	}
}

// A report goes straight to the store, so nothing else checks it. A negative
// ceiling is the case that matters: placement asks whether allocated is below
// capacity, so the Worker would take no Actor ever again.
func TestRegisterWorker_RejectsNonsense(t *testing.T) {
	st, cleanup := storetest.SetupTestStore(t)
	defer cleanup()
	s := New(st, &fakeControl{}, testAteletSPIFFEID, nil, "", nil)
	seeded := seedReportedWorker(t, st, testNode, &ateapipb.WorkerResources{Actors: 4094})
	authed := ateletauthtest.ContextWith(ateletauthtest.CertOn(t, testNode))

	for _, tc := range []struct {
		name     string
		capacity *workerservicepb.WorkerResources
	}{
		{"negative ceiling", &workerservicepb.WorkerResources{Actors: -1}},
		{"int32 underflow", &workerservicepb.WorkerResources{Actors: -2147483648}},
		{"negative quantity", &workerservicepb.WorkerResources{CpuMillicores: -1, MemoryBytes: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.RegisterWorker(authed, &workerservicepb.RegisterWorkerRequest{
				Worker:         &workerservicepb.ObjectRef{Name: testWorkerName},
				Capacity:       tc.capacity,
				DefaultRuntime: testDefaultRuntime,
			})
			if got := apierror.Code(err); got != codes.InvalidArgument {
				t.Fatalf("code = %v (err %v), want %v", got, err, codes.InvalidArgument)
			}
		})
	}

	after, err := st.GetWorker(context.Background(), testWorkerName)
	if err != nil {
		t.Fatalf("GetWorker: %v", err)
	}
	if diff := cmp.Diff(seeded.GetStatus().GetCapacity(), after.GetStatus().GetCapacity(), protocmp.Transform()); diff != "" {
		t.Errorf("capacity changed despite every report being refused (-want +got):\n%s", diff)
	}
}
