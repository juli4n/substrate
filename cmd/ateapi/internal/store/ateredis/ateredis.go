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

// Package ateredis is an ate storage backend built on Redis.
//
// Actors are stored in keys of the form `actor:<actor-id>`, serialized as
// protojson DBActor objects.
//
// Redis / Valkey in cluster mode requires that all keys touched by a single
// transaction hash to the same cluster slot. Actor operations are scoped to a
// single actor key so this is not a concern here.
//
// Note that Redis Lua is not ACID — a power failure may leave only part of a
// script's effects applied.
package ateredis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agent-substrate/substrate/cmd/ateapi/internal/store"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type redisClient interface {
	redis.Cmdable
	ForEachMaster(ctx context.Context, fn func(ctx context.Context, client *redis.Client) error) error
	Watch(ctx context.Context, fn func(*redis.Tx) error, keys ...string) error
}

// Persistence is a service that stores information about applications in Redis.
type Persistence struct {
	rdb redisClient
}

var _ store.Interface = (*Persistence)(nil)

// NewPersistence creates a new Persistence.
func NewPersistence(redisClient *redis.ClusterClient) *Persistence {
	return &Persistence{
		rdb: redisClient,
	}
}

func actorDBKey(id string) string {
	return "actor:" + id
}


// DebugClearAll flushes all data from Redis.
func (s *Persistence) DebugClearAll(ctx context.Context) error {
	// Iterate through every Primary (Master) node in the cluster
	err := s.rdb.ForEachMaster(ctx, func(ctx context.Context, master *redis.Client) error {
		// Log which shard we are currently flushing (optional but helpful for debugging)
		shardAddr := master.Options().Addr
		fmt.Printf("Flushing shard: %s\n", shardAddr)

		// Execute the flush on this specific shard
		return master.FlushAllAsync(ctx).Err()
	})
	return err
}

func (s *Persistence) GetActor(ctx context.Context, id string) (*ateapipb.Actor, error) {
	dbKey := actorDBKey(id)

	dbActorBytes, err := s.rdb.Get(ctx, dbKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("while getting actor key %q: %w", dbKey, err)
	}

	actor := &ateapipb.Actor{}
	if err := protojson.Unmarshal(dbActorBytes, actor); err != nil {
		return nil, fmt.Errorf("while unmarshaling actor: %w", err)
	}

	if actor.GetActorId() != id {
		return nil, fmt.Errorf("(impossible) mismatch between stored id and key id")
	}

	return actor, nil
}

func (s *Persistence) CreateActor(ctx context.Context, actor *ateapipb.Actor) error {
	dbKey := actorDBKey(actor.GetActorId())

	// Clone because we will update the version field, and we don't want to
	// stomp the caller's copy.
	dbActor := proto.Clone(actor).(*ateapipb.Actor)
	dbActor.Version = 1

	dbActorBytes, err := protojson.Marshal(dbActor)
	if err != nil {
		return fmt.Errorf("in protojson.Marshal: %w", err)
	}

	ok, err := s.rdb.SetNX(ctx, dbKey, dbActorBytes, 0).Result()
	if err != nil {
		return fmt.Errorf("while executing redis set: %w", err)
	}
	if !ok {
		return store.ErrAlreadyExists
	}

	return nil
}

func (s *Persistence) DeleteActor(ctx context.Context, id string) error {
	dbKey := actorDBKey(id)
	err := s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		currentVal, err := tx.Get(ctx, dbKey).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return store.ErrNotFound
			}
			return fmt.Errorf("while getting actor: %w", err)
		}

		currentActor := &ateapipb.Actor{}
		if err := protojson.Unmarshal(currentVal, currentActor); err != nil {
			return fmt.Errorf("in protojson.Unmarshal: %w", err)
		}

		if currentActor.GetStatus() != ateapipb.Actor_STATUS_SUSPENDED {
			return store.ErrFailedPrecondition
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Del(ctx, dbKey)
			return nil
		})
		return err
	}, dbKey)

	if err != nil {
		if errors.Is(err, redis.TxFailedErr) {
			return store.ErrPersistenceRetry
		}
		return err
	}

	return nil
}

func (s *Persistence) UpdateActor(ctx context.Context, actor *ateapipb.Actor, expectedVersion int64) error {
	dbKey := actorDBKey(actor.GetActorId())

	// Clone because we will update the version field, and we don't want to
	// stomp the caller's copy.
	dbActor := proto.Clone(actor).(*ateapipb.Actor)

	err := s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		currentVal, err := tx.Get(ctx, dbKey).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return fmt.Errorf("actor does not exist")
			}
			return fmt.Errorf("while getting actor: %w", err)
		}

		currentActor := &ateapipb.Actor{}
		if err := protojson.Unmarshal(currentVal, currentActor); err != nil {
			return fmt.Errorf("in protojson.Unmarshal: %w", err)
		}

		if currentActor.GetVersion() != expectedVersion {
			return store.ErrPersistenceRetry
		}
		dbActor.Version = currentActor.GetVersion() + 1
		if currentActor.GetActorId() != dbActor.GetActorId() {
			return fmt.Errorf("actor_id is immutable")
		}
		if currentActor.GetActorTemplateNamespace() != dbActor.GetActorTemplateNamespace() {
			return fmt.Errorf("actor_template_namespace is immutable")
		}
		if currentActor.GetActorTemplateName() != dbActor.GetActorTemplateName() {
			return fmt.Errorf("actor_template_name is immutable")
		}

		newVal, err := protojson.Marshal(dbActor)
		if err != nil {
			return fmt.Errorf("in protojson.Marshal: %w", err)
		}

		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, dbKey, newVal, 0)
			return nil
		})
		return err
	}, dbKey)

	if err != nil {
		if errors.Is(err, store.ErrPersistenceRetry) || errors.Is(err, redis.TxFailedErr) {
			return store.ErrPersistenceRetry
		}
		return fmt.Errorf("while executing update actor transaction: %w", err)
	}

	actor.Version = dbActor.Version
	return nil
}

func (s *Persistence) ListActors(ctx context.Context) ([]*ateapipb.Actor, error) {
	var result []*ateapipb.Actor
	var mu sync.Mutex

	err := s.rdb.ForEachMaster(ctx, func(ctx context.Context, master *redis.Client) error {
		iter := master.Scan(ctx, 0, "actor:*", 0).Iterator()
		for iter.Next(ctx) {
			actorKey := iter.Val()
			parts := strings.Split(actorKey, ":")
			if len(parts) != 2 {
				return fmt.Errorf("bad key format %q", actorKey)
			}

			getCmd := master.Get(ctx, actorKey)
			if getCmd.Err() != nil {
				return fmt.Errorf("while getting actor %q: %w", actorKey, getCmd.Err())
			}

			actor := &ateapipb.Actor{}
			if err := protojson.Unmarshal([]byte(getCmd.Val()), actor); err != nil {
				return fmt.Errorf("in protojson.Unmarshal: %w", err)
			}

			mu.Lock()
			result = append(result, actor)
			mu.Unlock()
		}
		return iter.Err()
	})

	if err != nil {
		return nil, fmt.Errorf("while iterating all redis master: %w", err)
	}
	return result, nil
}

func (s *Persistence) AcquireLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	ok, err := s.rdb.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("while acquiring lock for %q: %w", key, err)
	}
	return ok, nil
}

func (s *Persistence) ReleaseLock(ctx context.Context, key string, value string) error {
	var luaRelease = redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`)

	_, err := luaRelease.Run(ctx, s.rdb, []string{key}, value).Result()
	if err != nil {
		return fmt.Errorf("while releasing lock for %q with value %q: %w", key, value, err)
	}
	return nil
}
