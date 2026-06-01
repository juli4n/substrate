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
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/agent-substrate/substrate/internal/proto/ateletpb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrAteletNotFound is returned when no atelet pod is found for a given node.
var ErrAteletNotFound = errors.New("atelet pod not found for node")

type poolKey struct {
	namespace string
	name      string
}

// nodeCapacity tracks pod counts per worker pool for one atelet node.
type nodeCapacity struct {
	free  map[poolKey]int
	total map[poolKey]int
}

// AteletManager owns per-atelet gRPC connections, subscribes to WatchCapacity
// streams, and maintains an in-memory view of free worker pod capacity per
// node and pool. Connections are opened when an atelet pod gains an IP and
// closed when the pod is deleted, so the connection lifecycle mirrors the pod
// lifecycle exactly.
type AteletManager struct {
	ateletIndexer cache.Indexer

	mu       sync.RWMutex
	capacity map[string]*nodeCapacity

	connsMu sync.RWMutex
	conns   map[string]*grpc.ClientConn
	cancels map[string]context.CancelFunc
}

// NewAteletManager creates an AteletManager and wires it to the atelet pod
// informer. Subscriptions start automatically as pods gain IPs.
func NewAteletManager(ctx context.Context, ateletInformer cache.SharedIndexInformer) *AteletManager {
	am := &AteletManager{
		ateletIndexer: ateletInformer.GetIndexer(),
		capacity:      make(map[string]*nodeCapacity),
		conns:         make(map[string]*grpc.ClientConn),
		cancels:       make(map[string]context.CancelFunc),
	}

	ateletInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok || pod.Spec.NodeName == "" || pod.Status.PodIP == "" {
				return
			}
			am.startSubscription(ctx, pod.Spec.NodeName)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod, ok := oldObj.(*corev1.Pod)
			if !ok {
				return
			}
			newPod, ok := newObj.(*corev1.Pod)
			if !ok || newPod.Spec.NodeName == "" {
				return
			}
			// Restart only when the pod IP first appears (Pending→Running) or
			// changes, so a stale pod IP is never used after a pod replacement.
			if oldPod.Status.PodIP != newPod.Status.PodIP && newPod.Status.PodIP != "" {
				am.startSubscription(ctx, newPod.Spec.NodeName)
			}
		},
		DeleteFunc: func(obj interface{}) {
			var pod *corev1.Pod
			switch t := obj.(type) {
			case *corev1.Pod:
				pod = t
			case cache.DeletedFinalStateUnknown:
				var ok bool
				pod, ok = t.Obj.(*corev1.Pod)
				if !ok {
					return
				}
			}
			am.stopSubscription(pod.Spec.NodeName)
		},
	})

	return am
}

// ConnForNode returns the active gRPC connection to the atelet on nodeName.
// Returns ErrAteletNotFound if no connection exists (atelet not yet ready or
// already gone).
func (am *AteletManager) ConnForNode(nodeName string) (*grpc.ClientConn, error) {
	am.connsMu.RLock()
	defer am.connsMu.RUnlock()
	conn, ok := am.conns[nodeName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAteletNotFound, nodeName)
	}
	return conn, nil
}

// startSubscription (re)dials the atelet on nodeName, cancelling any prior
// subscription first so stale connections are never reused after a pod
// replacement.
func (am *AteletManager) startSubscription(parentCtx context.Context, nodeName string) {
	am.connsMu.Lock()

	if cancel, ok := am.cancels[nodeName]; ok {
		cancel()
	}
	if old, ok := am.conns[nodeName]; ok {
		old.Close() //nolint:errcheck
	}

	conn, err := am.dialNode(nodeName)
	if err != nil {
		delete(am.cancels, nodeName)
		delete(am.conns, nodeName)
		am.connsMu.Unlock()
		slog.Warn("AteletManager: failed to dial atelet", slog.String("node", nodeName), slog.Any("err", err))
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	am.conns[nodeName] = conn
	am.cancels[nodeName] = cancel
	am.connsMu.Unlock()

	go am.subscribeToNode(ctx, nodeName)
}

// stopSubscription cancels the subscription goroutine, closes the connection,
// and removes the node's capacity entry.
func (am *AteletManager) stopSubscription(nodeName string) {
	am.connsMu.Lock()
	if cancel, ok := am.cancels[nodeName]; ok {
		cancel()
		delete(am.cancels, nodeName)
	}
	if conn, ok := am.conns[nodeName]; ok {
		conn.Close() //nolint:errcheck
		delete(am.conns, nodeName)
	}
	am.connsMu.Unlock()

	am.mu.Lock()
	delete(am.capacity, nodeName)
	am.mu.Unlock()
}

// dialNode looks up the atelet pod for nodeName and creates a gRPC connection
// to it. grpc.NewClient is non-blocking; the physical connection is established
// lazily.
func (am *AteletManager) dialNode(nodeName string) (*grpc.ClientConn, error) {
	atelets, err := am.ateletIndexer.ByIndex(byNode, nodeName)
	if err != nil {
		return nil, fmt.Errorf("while finding atelet for node %q: %w", nodeName, err)
	}
	if len(atelets) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrAteletNotFound, nodeName)
	}
	if len(atelets) > 1 {
		return nil, fmt.Errorf("found %d atelet pods on node %q, expected 1", len(atelets), nodeName)
	}
	pod := atelets[0].(*corev1.Pod)
	if len(pod.Status.PodIPs) == 0 {
		return nil, fmt.Errorf("atelet pod %s/%s has no assigned IPs", pod.Namespace, pod.Name)
	}
	conn, err := grpc.NewClient(
		pod.Status.PodIPs[0].IP+":8085",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("while dialing atelet on node %s: %w", nodeName, err)
	}
	return conn, nil
}

// subscribeToNode opens a WatchCapacity stream and keeps it alive with
// reconnects until ctx is cancelled.
func (am *AteletManager) subscribeToNode(ctx context.Context, nodeName string) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := am.runStream(ctx, nodeName); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "capacity stream error, reconnecting",
				slog.String("node", nodeName), slog.Any("err", err))
		}

		// Clear stale capacity for this node on disconnect so we don't
		// over-schedule while reconnecting.
		am.mu.Lock()
		delete(am.capacity, nodeName)
		am.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (am *AteletManager) runStream(ctx context.Context, nodeName string) error {
	conn, err := am.ConnForNode(nodeName)
	if err != nil {
		return err
	}
	client := ateletpb.NewAteomHerderClient(conn)
	stream, err := client.WatchCapacity(ctx, &ateletpb.WatchCapacityRequest{})
	if err != nil {
		return err
	}

	for {
		snap, err := stream.Recv()
		if err != nil {
			return err
		}

		nc := &nodeCapacity{
			free:  make(map[poolKey]int, len(snap.GetPools())),
			total: make(map[poolKey]int, len(snap.GetPools())),
		}
		for _, p := range snap.GetPools() {
			pk := poolKey{namespace: p.GetPoolNamespace(), name: p.GetPoolName()}
			nc.free[pk] = int(p.GetFree())
			nc.total[pk] = int(p.GetTotal())
		}

		am.mu.Lock()
		am.capacity[nodeName] = nc
		am.mu.Unlock()
	}
}

// PoolDump holds free and total pod counts for a single pool.
type PoolDump struct {
	Free  int `json:"free"`
	Total int `json:"total"`
}

// NodeDump holds capacity counts for all pools on a single node.
type NodeDump struct {
	// Pools maps "poolNamespace/poolName" to its capacity counts.
	Pools map[string]PoolDump `json:"pools"`
}

// CapacityDump is the JSON structure returned by Dump.
type CapacityDump struct {
	// Nodes maps node name to its pool capacity.
	Nodes map[string]NodeDump `json:"nodes"`
}

// Dump returns a snapshot of the current capacity state. Intended for debugging.
func (am *AteletManager) Dump() CapacityDump {
	am.mu.RLock()
	defer am.mu.RUnlock()
	dump := CapacityDump{Nodes: make(map[string]NodeDump, len(am.capacity))}
	for nodeName, nc := range am.capacity {
		pools := make(map[string]PoolDump, len(nc.free))
		for pk, free := range nc.free {
			pools[pk.namespace+"/"+pk.name] = PoolDump{Free: free, Total: nc.total[pk]}
		}
		dump.Nodes[nodeName] = NodeDump{Pools: pools}
	}
	return dump
}

// ForceCapacity directly sets the free and total counts for a node/pool.
// Intended for testing.
func (am *AteletManager) ForceCapacity(nodeName, poolNamespace, poolName string, freeCount int) {
	pk := poolKey{namespace: poolNamespace, name: poolName}
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.capacity[nodeName] == nil {
		am.capacity[nodeName] = &nodeCapacity{
			free:  make(map[poolKey]int),
			total: make(map[poolKey]int),
		}
	}
	am.capacity[nodeName].free[pk] = freeCount
	am.capacity[nodeName].total[pk] = freeCount
}

// CandidateNodes returns atelet node names that have free capacity for the
// given pool. The preferred node is returned first if it has capacity.
// Remaining nodes are sorted by a capacity-weighted random score.
func (am *AteletManager) CandidateNodes(poolNamespace, poolName, preferredNode string) []string {
	pk := poolKey{namespace: poolNamespace, name: poolName}

	am.mu.RLock()
	defer am.mu.RUnlock()

	type candidate struct {
		name  string
		score float64
	}

	var preferred []string
	var others []candidate
	for nodeName, nc := range am.capacity {
		free := nc.free[pk]
		if free <= 0 {
			continue
		}
		if nodeName == preferredNode {
			preferred = append(preferred, nodeName)
			continue
		}
		others = append(others, candidate{
			name:  nodeName,
			score: float64(free) * (0.5 + rand.Float64()),
		})
	}

	sort.Slice(others, func(i, j int) bool {
		return others[i].score > others[j].score
	})

	result := preferred
	for _, c := range others {
		result = append(result, c.name)
	}
	return result
}
