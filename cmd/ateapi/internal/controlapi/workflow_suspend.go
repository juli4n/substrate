//  Copyright 2026 Google LLC
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package controlapi

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	listersv1alpha1 "github.com/agent-substrate/substrate/pkg/client/listers/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
)

// SuspendInput holds the immutable parameters requested by the client.
type SuspendInput struct {
	ActorID string
}

// SuspendState holds the mutable state loaded and modified during execution.
type SuspendState struct {
	Actor         *ateapipb.Actor
	ActorTemplate *atev1alpha1.ActorTemplate
}

type LoadActorForSuspendStep struct {
	store               store.Interface
	actorTemplateLister listersv1alpha1.ActorTemplateLister
}

func (s *LoadActorForSuspendStep) Name() string { return "LoadActorForSuspend" }
func (s *LoadActorForSuspendStep) IsComplete(ctx context.Context, input *SuspendInput, state *SuspendState) (bool, error) {
	// Always run to get the freshest state
	return false, nil
}
func (s *LoadActorForSuspendStep) Execute(ctx context.Context, input *SuspendInput, state *SuspendState) error {
	actor, err := s.store.GetActor(ctx, input.ActorID)
	if err != nil {
		return err
	}
	state.Actor = actor

	actorTemplate, err := s.actorTemplateLister.ActorTemplates(actor.GetActorTemplateNamespace()).Get(actor.GetActorTemplateName())
	if err != nil {
		return fmt.Errorf("while getting ActorTemplate: %w", err)
	}
	state.ActorTemplate = actorTemplate

	return nil
}



type MarkSuspendingStep struct {
	store store.Interface
}

func (s *MarkSuspendingStep) Name() string { return "MarkSuspending" }
func (s *MarkSuspendingStep) IsComplete(ctx context.Context, input *SuspendInput, state *SuspendState) (bool, error) {
	// Fast forward if we've already marked our intent or if we are further along.
	return state.Actor.GetStatus() == ateapipb.Actor_STATUS_SUSPENDING || state.Actor.GetStatus() == ateapipb.Actor_STATUS_SUSPENDED, nil
}
func (s *MarkSuspendingStep) Execute(ctx context.Context, input *SuspendInput, state *SuspendState) error {
	if state.Actor.GetStatus() != ateapipb.Actor_STATUS_RUNNING {
		return nil
	}

	state.Actor.Status = ateapipb.Actor_STATUS_SUSPENDING
	snapshotID := time.Now().Format(time.RFC3339) + "-" + rand.Text()
	state.Actor.InProgressSnapshot = strings.TrimSuffix(state.ActorTemplate.Spec.SnapshotsConfig.Location, "/") + "/" + input.ActorID + "/" + snapshotID
	return s.store.UpdateActor(ctx, state.Actor, state.Actor.GetVersion())
}



type CallAteletSuspendStep struct {
	ateletManager *AteletManager
}

func (s *CallAteletSuspendStep) Name() string { return "CallAteletSuspend" }
func (s *CallAteletSuspendStep) IsComplete(ctx context.Context, input *SuspendInput, state *SuspendState) (bool, error) {
	// If we are already SUSPENDED, we've already called Atelet
	return state.Actor.GetStatus() == ateapipb.Actor_STATUS_SUSPENDED, nil
}
func (s *CallAteletSuspendStep) Execute(ctx context.Context, input *SuspendInput, state *SuspendState) error {
	if state.Actor.GetLastNodeName() == "" {
		return fmt.Errorf("actor is in SUSPENDING state but has no recorded node")
	}

	ateletConn, err := s.ateletManager.ConnForNode(state.Actor.GetLastNodeName())
	if err != nil {
		return fmt.Errorf("while dialing atelet on node %s: %w", state.Actor.GetLastNodeName(), err)
	}
	client := ateletpb.NewAteomHerderClient(ateletConn)

	req := &ateletpb.CheckpointRequest{
		ActorTemplateNamespace: state.Actor.GetActorTemplateNamespace(),
		ActorTemplateName:      state.Actor.GetActorTemplateName(),
		ActorId:                state.Actor.GetActorId(),
		Runsc:                  buildRunscConfig(state.ActorTemplate),
		Spec:                   buildWorkloadSpec(state.ActorTemplate),
		SnapshotUriPrefix:      state.Actor.GetInProgressSnapshot(),
	}
	_, err = client.Checkpoint(ctx, req)
	if err != nil {
		return fmt.Errorf("while checkpointing workload: %w", err)
	}

	return nil
}



type FinalizeSuspendedStep struct {
	store store.Interface
}

func (s *FinalizeSuspendedStep) Name() string { return "FinalizeSuspended" }
func (s *FinalizeSuspendedStep) IsComplete(_ context.Context, _ *SuspendInput, state *SuspendState) (bool, error) {
	return state.Actor.GetStatus() == ateapipb.Actor_STATUS_SUSPENDED, nil
}
func (s *FinalizeSuspendedStep) Execute(ctx context.Context, input *SuspendInput, state *SuspendState) error {
	latestActor, err := s.store.GetActor(ctx, input.ActorID)
	if err != nil {
		return err
	}

	latestActor.Status = ateapipb.Actor_STATUS_SUSPENDED
	if latestActor.InProgressSnapshot != "" {
		latestActor.LastSnapshot = latestActor.InProgressSnapshot
		latestActor.InProgressSnapshot = ""
	}
	// Clear active pod fields but preserve last_node_name for placement affinity
	// on the next resume.
	latestActor.AteomPodNamespace = ""
	latestActor.AteomPodName = ""
	latestActor.AteomPodIp = ""

	if err := s.store.UpdateActor(ctx, latestActor, latestActor.GetVersion()); err != nil {
		return err
	}
	state.Actor = latestActor
	return nil
}


