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
	"log/slog"

	"go.ate.dev/substrate/cmd/ateapi/internal/ateletauth"
	"go.ate.dev/substrate/internal/proto/workerservicepb"
	"go.ate.dev/substrate/internal/resources"
)

// ReportActorCrashed crashes a RUNNING Actor whose sandbox died, on behalf of
// the Worker hosting it. The caller must be an atelet running on the Worker's
// node.
func (s *Server) ReportActorCrashed(ctx context.Context, req *workerservicepb.ReportActorCrashedRequest) (*workerservicepb.ReportActorCrashedResponse, error) {
	caller, err := ateletauth.Authenticate(ctx, s.ateletSPIFFEID)
	if err != nil {
		return nil, err
	}
	if errs := validateReportActorCrashedRequest(ctx, req); len(errs) > 0 {
		return nil, resources.ToAPIError(errs)
	}

	workerName := req.GetWorker().GetName()
	actorRef := actorRefFromObjectRef(req.GetActor())
	if err := s.authorizeHostedActor(ctx, caller, workerName, actorRef, req.GetActorUid()); err != nil {
		return nil, err
	}
	if _, err := s.control.CrashActorReportedByWorker(ctx, actorRef, req.GetActorUid(), workerName, req.GetMessage()); err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "Worker reported an actor crash",
		slog.String("worker", workerName),
		slog.String("actor", actorRef.String()),
		slog.String("actor_uid", req.GetActorUid()),
		slog.String("node", caller.NodeName),
		slog.String("message", req.GetMessage()))
	return &workerservicepb.ReportActorCrashedResponse{}, nil
}
