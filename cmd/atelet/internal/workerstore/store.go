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

package workerstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	bolt "go.etcd.io/bbolt"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// ErrNoFreeWorkerPods is returned by AssignWorkerPod when no free pods are
// available for the requested pool. Callers at the gRPC boundary should map
// this to codes.ResourceExhausted.
var ErrNoFreeWorkerPods = errors.New("no free worker pods for pool")

// WorkerPodUID is the unique identifier of a worker pod. Pod names can be
// reused across generations; the UID is stable for the lifetime of a pod and
// never reused.
type WorkerPodUID = k8stypes.UID

// WorkerPodName identifies a worker pod by namespace and name.
type WorkerPodName = k8stypes.NamespacedName

// WorkerPoolName identifies a worker pool by namespace and name.
type WorkerPoolName = k8stypes.NamespacedName

var (
	bucketPodState        = []byte("pod_state")
	bucketActorAssignment = []byte("actor_assignment")
)

// WorkerPodEntry is the value persisted in the pod_state bucket.
type WorkerPodEntry struct {
	PoolNamespace string       `json:"pool_namespace"`
	PoolName      string       `json:"pool_name"`
	PodUID        WorkerPodUID `json:"pod_uid"`
	PodName       string       `json:"pod_name"`
	PodNamespace  string       `json:"pod_namespace"`
	PodIP         string       `json:"pod_ip"`
	ActorID       string       `json:"actor_id,omitempty"`
}

// AssignmentEntry is the value persisted in the actor_assignment bucket.
type AssignmentEntry struct {
	PodUID       WorkerPodUID `json:"pod_uid"`
	PodName      string       `json:"pod_name"`
	PodNamespace string       `json:"pod_namespace"`
	PodIP        string       `json:"pod_ip"`
}

// StoreDump is returned by Store.Dump. Intended for debugging.
type StoreDump struct {
	Pods        map[string]WorkerPodEntry  `json:"pods"`
	Assignments map[string]AssignmentEntry `json:"assignments"`
}

// Interface defines the contract for the worker pod store.
type Interface interface {
	RegisterWorkerPod(workerPodUID WorkerPodUID, pod WorkerPodName, podIP string, pool WorkerPoolName) error
	UnregisterWorkerPod(workerPodUID WorkerPodUID) error
	AssignWorkerPod(actorID string, pool WorkerPoolName) (WorkerPodName, string, error)
	GetWorkerPodAssignment(actorID string) (WorkerPodName, string, bool, error)
	UnassignWorkerPod(actorID string) error
	WorkerPodUIDs() ([]WorkerPodUID, error)
	CapacitySnapshot() (*ateletpb.CapacitySnapshot, error)
	Dump() (*StoreDump, error)
	Close() error
}

// Store manages worker pod state in bbolt.
// Keys in pod_state: pod UID.
// Keys in actor_assignment: actor ID.
type Store struct {
	db          *bolt.DB
	broadcaster *Broadcaster
}

// NewStore opens (or creates) the bbolt database at path and returns a Store.
func NewStore(path string, broadcaster *Broadcaster) (*Store, error) {
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("opening bbolt db: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketPodState); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketActorAssignment)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("creating bbolt buckets: %w", err)
	}
	return &Store{db: db, broadcaster: broadcaster}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// RegisterWorkerPod records a new worker pod. Idempotent: if the pod is already
// registered (same UID) it is a no-op. Publishes a capacity signal when a new
// pod is added.
func (s *Store) RegisterWorkerPod(workerPodUID WorkerPodUID, pod WorkerPodName, podIP string, pool WorkerPoolName) error {
	key := string(workerPodUID)
	rec := WorkerPodEntry{
		PoolNamespace: pool.Namespace,
		PoolName:      pool.Name,
		PodUID:        workerPodUID,
		PodName:       pod.Name,
		PodNamespace:  pod.Namespace,
		PodIP:         podIP,
	}
	var capacityChanged bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPodState)
		if existing := b.Get([]byte(key)); existing != nil {
			var prev WorkerPodEntry
			if err := json.Unmarshal(existing, &prev); err != nil {
				return fmt.Errorf("unmarshal existing pod entry for UID %s: %w", workerPodUID, err)
			}
			if prev.PodName != rec.PodName || prev.PodNamespace != rec.PodNamespace ||
				prev.PodIP != rec.PodIP || prev.PoolName != rec.PoolName || prev.PoolNamespace != rec.PoolNamespace {
				return fmt.Errorf("pod UID %s already registered with different values: stored=%+v incoming=%+v", workerPodUID, prev, rec)
			}
			return nil
		}
		val, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(key), val); err != nil {
			return err
		}
		capacityChanged = true
		return nil
	})
	if err != nil {
		return err
	}
	if capacityChanged {
		s.broadcaster.Publish()
	}
	return nil
}

// UnregisterWorkerPod removes a worker pod. If the pod had an actor assigned,
// the assignment record is also removed. Publishes a capacity signal when the
// pod was known.
func (s *Store) UnregisterWorkerPod(workerPodUID WorkerPodUID) error {
	key := string(workerPodUID)
	var capacityChanged bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketPodState)
		val := b.Get([]byte(key))
		if val == nil {
			return nil
		}
		var rec WorkerPodEntry
		if err := json.Unmarshal(val, &rec); err != nil {
			return err
		}
		if err := b.Delete([]byte(key)); err != nil {
			return err
		}
		if rec.ActorID != "" {
			if err := tx.Bucket(bucketActorAssignment).Delete([]byte(rec.ActorID)); err != nil {
				return err
			}
		}
		// Pod was found and removed, the capacity changes regardless of
		// whether it was free or assigned.
		capacityChanged = true
		return nil
	})
	if err != nil {
		return err
	}
	if capacityChanged {
		s.broadcaster.Publish()
	}
	return nil
}

// AssignWorkerPod atomically finds a free pod for the given pool and assigns it
// to actorID. Idempotent: if actorID already has an assignment on this node the
// existing pod is returned unchanged. Returns RESOURCE_EXHAUSTED when no free
// pods are available.
func (s *Store) AssignWorkerPod(actorID string, pool WorkerPoolName) (WorkerPodName, string, error) {
	var assigned WorkerPodName
	var podIP string
	var capacityChanged bool

	err := s.db.Update(func(tx *bolt.Tx) error {
		assignments := tx.Bucket(bucketActorAssignment)

		if existing := assignments.Get([]byte(actorID)); existing != nil {
			// actor already assigned on this node, return existing pod.
			var entry AssignmentEntry
			if err := json.Unmarshal(existing, &entry); err != nil {
				return err
			}
			assigned = WorkerPodName{Namespace: entry.PodNamespace, Name: entry.PodName}
			podIP = entry.PodIP
			return nil
		}

		pods := tx.Bucket(bucketPodState)
		var workerEntry *WorkerPodEntry
		c := pods.Cursor()
		for _, v := c.First(); v != nil; _, v = c.Next() {
			var entry WorkerPodEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return err
			}
			if entry.ActorID == "" && entry.PoolNamespace == pool.Namespace && entry.PoolName == pool.Name {
				workerEntry = &entry
				break
			}
		}

		if workerEntry == nil {
			return ErrNoFreeWorkerPods
		}

		workerEntry.ActorID = actorID
		val, err := json.Marshal(workerEntry)
		if err != nil {
			return err
		}
		if err := pods.Put([]byte(string(workerEntry.PodUID)), val); err != nil {
			return err
		}

		aVal, err := json.Marshal(AssignmentEntry{
			PodUID:       workerEntry.PodUID,
			PodName:      workerEntry.PodName,
			PodNamespace: workerEntry.PodNamespace,
			PodIP:        workerEntry.PodIP,
		})
		if err != nil {
			return err
		}
		if err := assignments.Put([]byte(actorID), aVal); err != nil {
			return err
		}

		assigned = WorkerPodName{Namespace: workerEntry.PodNamespace, Name: workerEntry.PodName}
		podIP = workerEntry.PodIP
		// A pod moved from free to assigned — capacity changed.
		capacityChanged = true
		return nil
	})
	if err != nil {
		return WorkerPodName{}, "", err
	}
	if capacityChanged {
		s.broadcaster.Publish()
	}
	return assigned, podIP, nil
}

// GetWorkerPodAssignment returns the pod currently assigned to actorID, or
// found=false if no assignment exists.
func (s *Store) GetWorkerPodAssignment(actorID string) (WorkerPodName, string, bool, error) {
	var pod WorkerPodName
	var podIP string
	var found bool

	err := s.db.View(func(tx *bolt.Tx) error {
		val := tx.Bucket(bucketActorAssignment).Get([]byte(actorID))
		if val == nil {
			return nil
		}
		var rec AssignmentEntry
		if err := json.Unmarshal(val, &rec); err != nil {
			return err
		}
		pod = WorkerPodName{Namespace: rec.PodNamespace, Name: rec.PodName}
		podIP = rec.PodIP
		found = true
		return nil
	})
	return pod, podIP, found, err
}

// UnassignWorkerPod releases the assignment held by actorID, returning the pod
// to the free pool. No-op if actorID has no assignment. Publishes a capacity
// signal when an assignment was released.
func (s *Store) UnassignWorkerPod(actorID string) error {
	var capacityChanged bool
	err := s.db.Update(func(tx *bolt.Tx) error {
		assignments := tx.Bucket(bucketActorAssignment)
		val := assignments.Get([]byte(actorID))
		if val == nil {
			return nil
		}
		var aRec AssignmentEntry
		if err := json.Unmarshal(val, &aRec); err != nil {
			return err
		}
		pods := tx.Bucket(bucketPodState)
		podVal := pods.Get([]byte(string(aRec.PodUID)))
		if podVal == nil {
			// Pod was already removed from pod_state (e.g. unregistered while
			// assigned). Clean up the dangling assignment; capacity is unchanged
			// since the pod was not contributing to the snapshot.
			return assignments.Delete([]byte(actorID))
		}
		var pRec WorkerPodEntry
		if err := json.Unmarshal(podVal, &pRec); err != nil {
			return err
		}
		pRec.ActorID = ""
		updated, err := json.Marshal(pRec)
		if err != nil {
			return err
		}
		if err := pods.Put([]byte(string(aRec.PodUID)), updated); err != nil {
			return err
		}
		if err := assignments.Delete([]byte(actorID)); err != nil {
			return err
		}
		// Pod returned to the free pool — capacity changed.
		capacityChanged = true
		return nil
	})
	if err != nil {
		return err
	}
	if capacityChanged {
		s.broadcaster.Publish()
	}
	return nil
}

// WorkerPodUIDs returns the UIDs of all worker pods currently tracked in the
// store.
func (s *Store) WorkerPodUIDs() ([]WorkerPodUID, error) {
	var uids []WorkerPodUID
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPodState).ForEach(func(k, _ []byte) error {
			uids = append(uids, WorkerPodUID(k))
			return nil
		})
	})
	return uids, err
}

// Dump returns a complete snapshot of the database. Intended for debugging.
func (s *Store) Dump() (*StoreDump, error) {
	dump := &StoreDump{
		Pods:        make(map[string]WorkerPodEntry),
		Assignments: make(map[string]AssignmentEntry),
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		if err := tx.Bucket(bucketPodState).ForEach(func(k, v []byte) error {
			var rec WorkerPodEntry
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			dump.Pods[string(k)] = rec
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket(bucketActorAssignment).ForEach(func(k, v []byte) error {
			var rec AssignmentEntry
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			dump.Assignments[string(k)] = rec
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return dump, nil
}

// CapacitySnapshot returns per-pool free and total pod counts for all pools on
// this node. Used by the WatchCapacity gRPC handler.
func (s *Store) CapacitySnapshot() (*ateletpb.CapacitySnapshot, error) {
	type counts struct{ free, total int32 }
	poolCounts := make(map[WorkerPoolName]*counts)

	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketPodState).ForEach(func(_, v []byte) error {
			var rec WorkerPodEntry
			if err := json.Unmarshal(v, &rec); err != nil {
				return err
			}
			pk := WorkerPoolName{Namespace: rec.PoolNamespace, Name: rec.PoolName}
			c := poolCounts[pk]
			if c == nil {
				c = &counts{}
				poolCounts[pk] = c
			}
			c.total++
			if rec.ActorID == "" {
				c.free++
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	snap := &ateletpb.CapacitySnapshot{Pools: make([]*ateletpb.PoolCapacity, 0, len(poolCounts))}
	for pk, c := range poolCounts {
		snap.Pools = append(snap.Pools, &ateletpb.PoolCapacity{
			PoolNamespace: pk.Namespace,
			PoolName:      pk.Name,
			Free:          c.free,
			Total:         c.total,
		})
	}
	return snap, nil
}

// Broadcaster notifies WatchCapacity subscribers that capacity has changed.
// It is signal-only: subscribers re-read the full snapshot on each signal.
type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[int]chan struct{}
	nextID      int
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: make(map[int]chan struct{})}
}

// Subscribe returns an ID and a channel that receives a struct{} whenever
// capacity changes. The channel has a buffer of 1; excess signals are dropped
// because the subscriber re-reads the full snapshot regardless.
func (b *Broadcaster) Subscribe() (int, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan struct{}, 1)
	b.subscribers[id] = ch
	return id, ch
}

// Unsubscribe removes and closes the subscriber channel for id.
func (b *Broadcaster) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}

// Publish sends a signal to all subscribers. Non-blocking: if a subscriber's
// channel is already full the signal is dropped (it already has a pending read).
func (b *Broadcaster) Publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
