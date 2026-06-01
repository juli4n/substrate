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

package ateredis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
)

func setupTest(t *testing.T) (*miniredis.Miniredis, *Persistence, context.Context) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	// Miniredis runs as a single node, but ClusterClient can work with it
	// if we don't use cluster-specific commands that miniredis doesn't support.
	// Miniredis supports most standard commands.
	rdb := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs: []string{mr.Addr()},
	})
	return mr, &Persistence{rdb: rdb}, context.Background()
}

func TestGetActor_NotFound(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	_, err := s.GetActor(ctx, "non-existent")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateActor_Success(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	got, err := s.GetActor(ctx, actor.ActorId)
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}

	expected := proto.Clone(actor).(*ateapipb.Actor)
	expected.Version = 1

	if diff := cmp.Diff(expected, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetActor returned unexpected actor (-want +got):\n%s", diff)
	}
}

func TestCreateActor_AlreadyExists(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	err = s.CreateActor(ctx, actor)
	if err == nil {
		t.Errorf("expected error creating existing actor, got nil")
	}
}

func TestUpdateActor_Success(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	actor.Status = ateapipb.Actor_STATUS_RUNNING
	actor.Version = 1

	err = s.UpdateActor(ctx, actor, 1)
	if err != nil {
		t.Fatalf("UpdateActor failed: %v", err)
	}

	if actor.Version != 2 {
		t.Errorf("expected actor.Version to be updated to 2, got %d", actor.Version)
	}

	updated, err := s.GetActor(ctx, actor.ActorId)
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}

	expected := proto.Clone(actor).(*ateapipb.Actor)

	if diff := cmp.Diff(expected, updated, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateActor yielded unexpected state in DB (-want +got):\n%s", diff)
	}
}

func TestUpdateActor_Conflict(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	// Fetch instance 1
	actor1, err := s.GetActor(ctx, actor.ActorId)
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}

	// Fetch instance 2 (stale after actor1 updates)
	actor2, err := s.GetActor(ctx, actor.ActorId)
	if err != nil {
		t.Fatalf("GetActor failed: %v", err)
	}

	// Update instance 1
	actor1.Status = ateapipb.Actor_STATUS_RUNNING
	err = s.UpdateActor(ctx, actor1, actor1.GetVersion())
	if err != nil {
		t.Fatalf("UpdateActor failed: %v", err)
	}

	// Try to update instance 2 (which has stale version)
	actor2.Status = ateapipb.Actor_STATUS_SUSPENDED
	err = s.UpdateActor(ctx, actor2, actor2.GetVersion())
	if !errors.Is(err, store.ErrPersistenceRetry) {
		t.Errorf("expected ErrPersistenceRetry, got %v", err)
	}
}

func TestDeleteActor(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	err = s.DeleteActor(ctx, "session-1")
	if err != nil {
		t.Fatalf("DeleteActor failed: %v", err)
	}

	_, err = s.GetActor(ctx, "session-1")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteActor_NotSuspended(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor := &ateapipb.Actor{
		ActorId:                "session-1",
		ActorTemplateNamespace: "default",
		ActorTemplateName:      "test-template",
		Status:                 ateapipb.Actor_STATUS_RUNNING,
	}

	err := s.CreateActor(ctx, actor)
	if err != nil {
		t.Fatalf("CreateActor failed: %v", err)
	}

	err = s.DeleteActor(ctx, "session-1")
	if !errors.Is(err, store.ErrFailedPrecondition) {
		t.Errorf("expected ErrFailedPrecondition deleting running actor, got %v", err)
	}
}

func TestDeleteActor_NotFound(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	err := s.DeleteActor(ctx, "non-existent")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound deleting non-existent actor, got %v", err)
	}
}

func TestListActors(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actor1 := &ateapipb.Actor{
		ActorId:                "id1",
		ActorTemplateNamespace: "ns1",
		ActorTemplateName:      "tmpl1",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
		LastSnapshot:           "gs://b1/f1",
	}
	actor2 := &ateapipb.Actor{
		ActorId:                "id2",
		ActorTemplateNamespace: "ns1",
		ActorTemplateName:      "tmpl1",
		Status:                 ateapipb.Actor_STATUS_SUSPENDED,
		LastSnapshot:           "gs://b1/f2",
	}

	if err := s.CreateActor(ctx, actor1); err != nil {
		t.Fatalf("failed to create actor1: %v", err)
	}
	if err := s.CreateActor(ctx, actor2); err != nil {
		t.Fatalf("failed to create actor2: %v", err)
	}

	actors, err := s.ListActors(ctx)
	if err != nil {
		t.Fatalf("ListActors failed: %v", err)
	}

	if len(actors) != 2 {
		t.Errorf("expected 2 actors, got %d", len(actors))
	}

	found1 := false
	found2 := false
	for _, a := range actors {
		if a.GetActorId() == "id1" {
			found1 = true
		}
		if a.GetActorId() == "id2" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("did not find all actors: found1=%t, found2=%t", found1, found2)
	}
}

func TestListActors_Empty(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	actors, err := s.ListActors(ctx)
	if err != nil {
		t.Fatalf("ListActors failed: %v", err)
	}

	if len(actors) != 0 {
		t.Errorf("expected 0 actors, got %d", len(actors))
	}
}

func TestAcquireLock_Success(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value := "token-1"
	wrongValue := "token-2"
	newValue := "token-3"
	ttl := 10 * time.Second

	// 1. Acquire lock
	acquired, err := s.AcquireLock(ctx, key, value, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Errorf("expected lock to be acquired")
	}

	// 2. Try to release with WRONG value
	err = s.ReleaseLock(ctx, key, wrongValue)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	// Verify it is STILL THERE by trying to acquire it again
	acquired, err = s.AcquireLock(ctx, key, newValue, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if acquired {
		t.Errorf("expected lock to still be held by token-1, but token-3 successfully acquired it!")
	}

	// 3. Try to release with CORRECT value
	err = s.ReleaseLock(ctx, key, value)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	// Verify it is GONE by trying to acquire it again!
	acquired, err = s.AcquireLock(ctx, key, newValue, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Errorf("expected lock to be free, but it could not be acquired!")
	}
}

func TestAcquireLock_Conflict(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value1 := "token-1"
	value2 := "token-2"
	ttl := 10 * time.Second

	acquired, err := s.AcquireLock(ctx, key, value1, ttl)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lock to be acquired")
	}

	acquired, err = s.AcquireLock(ctx, key, value2, ttl)
	if err != nil {
		t.Fatalf("second AcquireLock failed: %v", err)
	}
	if acquired {
		t.Errorf("expected second lock to fail (conflict)")
	}
}

func TestReleaseLock_Success(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value := "token-1"
	ttl := 10 * time.Second

	s.AcquireLock(ctx, key, value, ttl)

	err := s.ReleaseLock(ctx, key, value)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	// Verify it's gone
	if mr.Exists(key) {
		t.Errorf("expected lock to be deleted")
	}
}

func TestReleaseLock_Unsafe(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value1 := "token-1"
	value2 := "token-2"
	value3 := "token-3"
	ttl := 10 * time.Second

	s.AcquireLock(ctx, key, value1, ttl)

	// Try to release with WRONG token
	err := s.ReleaseLock(ctx, key, value2)
	if err != nil {
		t.Fatalf("ReleaseLock failed: %v", err)
	}

	// Verify it is STILL THERE by trying to acquire it again!
	acquired, err := s.AcquireLock(ctx, key, value3, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if acquired {
		t.Errorf("expected lock to still be held by token-1, but token-3 successfully acquired it!")
	}
}

func TestAcquireLock_TTLExpiration(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value1 := "token-1"
	value2 := "token-2"
	ttl := 5 * time.Second

	// 1. Acquire lock
	acquired, err := s.AcquireLock(ctx, key, value1, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Fatalf("expected lock to be acquired")
	}

	// 2. Fast-forward time past TTL
	mr.FastForward(6 * time.Second)

	// 3. Try to acquire again with different token
	acquired, err = s.AcquireLock(ctx, key, value2, ttl)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Errorf("expected lock to be acquired by token-2 after TTL expiration")
	}
}

func TestAcquireLock_NonReentry(t *testing.T) {
	mr, s, ctx := setupTest(t)
	defer mr.Close()

	key := "test-lock"
	value := "token-1"
	ttl := 10 * time.Second

	// 1. Acquire lock first time
	acquired, err := s.AcquireLock(ctx, key, value, ttl)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	if !acquired {
		t.Fatalf("expected first lock to be acquired")
	}

	// 2. Try to acquire lock again with SAME token
	acquired, err = s.AcquireLock(ctx, key, value, ttl)
	if err != nil {
		t.Fatalf("second AcquireLock failed: %v", err)
	}
	if acquired {
		t.Errorf("expected second lock acquisition to fail (non-reentrant)")
	}
}
