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
	"errors"
	"fmt"
	"log/slog"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store"
	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	listersv1alpha1 "github.com/agent-substrate/substrate/pkg/client/listers/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ResumeInput holds the immutable parameters requested by the client.
type ResumeInput struct {
	ActorID string
	Boot    bool
}

// ResumeState holds the mutable state loaded and modified during execution.
type ResumeState struct {
	Actor         *ateapipb.Actor
	ActorTemplate *atev1alpha1.ActorTemplate
}

type LoadActorForResumeStep struct {
	store               store.Interface
	actorTemplateLister listersv1alpha1.ActorTemplateLister
}

func (s *LoadActorForResumeStep) Name() string { return "LoadActorForResume" }
func (s *LoadActorForResumeStep) IsComplete(_ context.Context, _ *ResumeInput, _ *ResumeState) (bool, error) {
	return false, nil
}
func (s *LoadActorForResumeStep) Execute(ctx context.Context, input *ResumeInput, state *ResumeState) error {
	actor, err := s.store.GetActor(ctx, input.ActorID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return status.Errorf(codes.NotFound, "Actor %s not found", input.ActorID)
		}
		return fmt.Errorf("while getting actor from DB: %w", err)
	}
	state.Actor = actor

	actorTemplate, err := s.actorTemplateLister.ActorTemplates(actor.GetActorTemplateNamespace()).Get(actor.GetActorTemplateName())
	if err != nil {
		return fmt.Errorf("while getting ActorTemplate: %w", err)
	}
	state.ActorTemplate = actorTemplate

	return nil
}



// PlaceAndRunStep picks an atelet with free capacity, declares intent by
// writing RESUMING + last_node_name, calls Run or Restore (idempotent at
// atelet), and on success writes the actor to RUNNING with the assigned pod
// info. Retries against other atelets on RESOURCE_EXHAUSTED.
type PlaceAndRunStep struct {
	store         store.Interface
	ateletManager *AteletManager
}

func (s *PlaceAndRunStep) Name() string { return "PlaceAndRun" }
func (s *PlaceAndRunStep) IsComplete(_ context.Context, _ *ResumeInput, state *ResumeState) (bool, error) {
	return state.Actor.GetStatus() == ateapipb.Actor_STATUS_RUNNING, nil
}

func (s *PlaceAndRunStep) Execute(ctx context.Context, input *ResumeInput, state *ResumeState) error {
	poolNs := state.ActorTemplate.Spec.WorkerPoolRef.Namespace
	poolName := state.ActorTemplate.Spec.WorkerPoolRef.Name

	// Build the candidate list: declared-intent node first (crash recovery),
	// then preferred last node, then any other node with free capacity.
	var declared string
	if state.Actor.GetStatus() == ateapipb.Actor_STATUS_RESUMING {
		declared = state.Actor.GetLastNodeName()
	}
	candidates := s.ateletManager.CandidateNodes(poolNs, poolName, declared)
	if len(candidates) == 0 {
		return status.Errorf(codes.ResourceExhausted, "no atelets with free capacity for pool %s/%s", poolNs, poolName)
	}

	workloadSpec := buildWorkloadSpec(state.ActorTemplate)
	runscCfg := buildRunscConfig(state.ActorTemplate)

	for _, nodeName := range candidates {
		// Declare intent: write RESUMING with chosen node before calling atelet.
		// This survives crashes — on retry we'll try the same node first.
		if state.Actor.GetLastNodeName() != nodeName || state.Actor.GetStatus() != ateapipb.Actor_STATUS_RESUMING {
			latest, err := s.store.GetActor(ctx, input.ActorID)
			if err != nil {
				return err
			}
			latest.Status = ateapipb.Actor_STATUS_RESUMING
			latest.LastNodeName = nodeName
			if err := s.store.UpdateActor(ctx, latest, latest.GetVersion()); err != nil {
				return err
			}
			state.Actor = latest
		}

		conn, err := s.ateletManager.ConnForNode(nodeName)
		if err != nil {
			slog.WarnContext(ctx, "PlaceAndRun: could not dial atelet, trying next", slog.String("node", nodeName), slog.Any("err", err))
			continue
		}
		client := ateletpb.NewAteomHerderClient(conn)

		podNs, podName, podIP, err := s.callAtelet(ctx, client, input, state, poolNs, poolName, workloadSpec, runscCfg)
		if err != nil {
			if status.Code(err) == codes.ResourceExhausted {
				slog.InfoContext(ctx, "PlaceAndRun: RESOURCE_EXHAUSTED, trying next node", slog.String("node", nodeName))
				continue
			}
			return fmt.Errorf("atelet %s returned error: %w", nodeName, err)
		}

		// Success: write actor to RUNNING with the pod atelet assigned.
		latest, err := s.store.GetActor(ctx, input.ActorID)
		if err != nil {
			return err
		}
		latest.Status = ateapipb.Actor_STATUS_RUNNING
		latest.LastNodeName = nodeName
		latest.AteomPodNamespace = podNs
		latest.AteomPodName = podName
		latest.AteomPodIp = podIP
		if err := s.store.UpdateActor(ctx, latest, latest.GetVersion()); err != nil {
			return err
		}
		state.Actor = latest
		return nil
	}

	return status.Errorf(codes.ResourceExhausted, "all candidate atelets exhausted for pool %s/%s", poolNs, poolName)
}

func (s *PlaceAndRunStep) callAtelet(
	ctx context.Context,
	client ateletpb.AteomHerderClient,
	input *ResumeInput,
	state *ResumeState,
	poolNs, poolName string,
	workloadSpec *ateletpb.WorkloadSpec,
	runscCfg *ateletpb.RunscConfig,
) (podNs, podName, podIP string, err error) {
	if state.Actor.LastSnapshot != "" {
		slog.InfoContext(ctx, "Restoring actor from snapshot")
		resp, err := client.Restore(ctx, &ateletpb.RestoreRequest{
			ActorTemplateNamespace: state.Actor.GetActorTemplateNamespace(),
			ActorTemplateName:      state.Actor.GetActorTemplateName(),
			ActorId:                state.Actor.GetActorId(),
			Runsc:                  runscCfg,
			Spec:                   workloadSpec,
			SnapshotUriPrefix:      state.Actor.GetLastSnapshot(),
			WorkerPoolNamespace:    poolNs,
			WorkerPoolName:         poolName,
		})
		if err != nil {
			return "", "", "", err
		}
		return resp.GetWorkerPodNamespace(), resp.GetWorkerPodName(), resp.GetWorkerPodIp(), nil
	}

	if state.ActorTemplate.Status.GoldenSnapshot != "" && !input.Boot {
		slog.InfoContext(ctx, "Restoring actor from golden snapshot")
		resp, err := client.Restore(ctx, &ateletpb.RestoreRequest{
			ActorTemplateNamespace: state.Actor.GetActorTemplateNamespace(),
			ActorTemplateName:      state.Actor.GetActorTemplateName(),
			ActorId:                state.Actor.GetActorId(),
			Runsc:                  runscCfg,
			Spec:                   workloadSpec,
			SnapshotUriPrefix:      state.ActorTemplate.Status.GoldenSnapshot,
			WorkerPoolNamespace:    poolNs,
			WorkerPoolName:         poolName,
		})
		if err != nil {
			return "", "", "", err
		}
		return resp.GetWorkerPodNamespace(), resp.GetWorkerPodName(), resp.GetWorkerPodIp(), nil
	}

	slog.InfoContext(ctx, "Booting actor from scratch")
	resp, err := client.Run(ctx, &ateletpb.RunRequest{
		ActorTemplateNamespace: state.Actor.GetActorTemplateNamespace(),
		ActorTemplateName:      state.Actor.GetActorTemplateName(),
		ActorId:                state.Actor.GetActorId(),
		Runsc:                  runscCfg,
		Spec:                   workloadSpec,
		WorkerPoolNamespace:    poolNs,
		WorkerPoolName:         poolName,
	})
	if err != nil {
		return "", "", "", err
	}
	return resp.GetWorkerPodNamespace(), resp.GetWorkerPodName(), resp.GetWorkerPodIp(), nil
}



func buildWorkloadSpec(t *atev1alpha1.ActorTemplate) *ateletpb.WorkloadSpec {
	spec := &ateletpb.WorkloadSpec{PauseImage: t.Spec.PauseImage}
	for _, ctr := range t.Spec.Containers {
		ac := &ateletpb.Container{
			Name:    ctr.Name,
			Image:   ctr.Image,
			Command: ctr.Command,
		}
		for _, env := range ctr.Env {
			ac.Env = append(ac.Env, &ateletpb.EnvEntry{Name: env.Name, Value: env.Value})
		}
		spec.Containers = append(spec.Containers, ac)
	}
	return spec
}

func buildRunscConfig(t *atev1alpha1.ActorTemplate) *ateletpb.RunscConfig {
	cfg := &ateletpb.RunscConfig{}
	if t.Spec.Runsc.AMD64 != nil {
		cfg.Amd64 = &ateletpb.RunscPlatformConfig{
			Sha256Hash: t.Spec.Runsc.AMD64.SHA256Hash,
			Url:        t.Spec.Runsc.AMD64.URL,
		}
	}
	if t.Spec.Runsc.ARM64 != nil {
		cfg.Arm64 = &ateletpb.RunscPlatformConfig{
			Sha256Hash: t.Spec.Runsc.ARM64.SHA256Hash,
			Url:        t.Spec.Runsc.ARM64.URL,
		}
	}
	if t.Spec.Runsc.Authentication.GCP != nil {
		cfg.Authentication = &ateletpb.AuthenticationConfig{
			Gcp: &ateletpb.GCPAuthenticationConfig{Use: true},
		}
	}
	return cfg
}
