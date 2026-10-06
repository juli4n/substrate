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

package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gateTimeout bounds how long Gate.Wait waits for the blocked call to arrive.
const gateTimeout = 10 * time.Second

// Fault decides how the calls to one operation of a fake behave. Its faults
// apply to the next calls, one each, in the order they were added. A call with
// no fault left succeeds.
type Fault struct {
	mu    sync.Mutex
	queue []*action
}

// action is one queued fault: fail with err, or hold the call at gate.
type action struct {
	err  error
	gate *Gate
}

// outcome is what a fake does with one call: apply its effect or not, and
// return err.
type outcome struct {
	apply bool
	err   error
}

// FailNext fails the next call with err, without applying its effect.
func (f *Fault) FailNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, &action{err: err})
}

// Block holds the next call until the returned Gate is released.
func (f *Fault) Block() *Gate {
	g := &Gate{arrived: make(chan struct{}, 1), released: make(chan struct{}, 1)}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, &action{gate: g})
	return g
}

// decide picks the outcome of one call to the faulted operation. It blocks
// while the call is held by a Gate, and fails with the context's error if ctx
// ends first.
func (f *Fault) decide(ctx context.Context) outcome {
	f.mu.Lock()
	var next *action
	if len(f.queue) > 0 {
		next = f.queue[0]
		f.queue = f.queue[1:]
	}
	f.mu.Unlock()

	switch {
	case next == nil:
		return outcome{apply: true}
	case next.gate != nil:
		next.gate.arrived <- struct{}{}
		select {
		case <-next.gate.released:
			return outcome{apply: true}
		case <-ctx.Done():
			return outcome{err: status.FromContextError(ctx.Err()).Err()}
		}
	default:
		return outcome{err: next.err}
	}
}

// Gate holds one call to a faulted operation until the test releases it.
type Gate struct {
	arrived  chan struct{}
	released chan struct{}
}

// Wait blocks until the held call arrives.
func (g *Gate) Wait(t testing.TB) {
	t.Helper()
	select {
	case <-g.arrived:
	case <-time.After(gateTimeout):
		t.Fatalf("no call reached the gate within %v", gateTimeout)
	}
}

// Release lets the held call proceed as if it had not been faulted.
func (g *Gate) Release() {
	g.released <- struct{}{}
}

// Error presets, named after the failures a real atelet or volume plugin
// reports. A real atelet wraps most failures with fmt.Errorf, which reaches
// ate-api as codes.Unknown.
var (
	// ErrSandboxFailed is the sandbox itself failing an operation: ateom or
	// runsc returned an error.
	ErrSandboxFailed = status.Error(codes.Unknown, "harness: sandbox operation failed")
	// ErrUnavailable is the transport failing: atelet is restarting or
	// unreachable.
	ErrUnavailable = status.Error(codes.Unavailable, "harness: unavailable")
)
