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

package ateom

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/agent-substrate/substrate/internal/ateompath"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/utils/lru"
)

// Dialer manages gRPC connections to ateom pods over UDS, caching up to 256
// connections and closing evicted ones.
type Dialer struct {
	conns *lru.Cache
}

// NewDialer creates a Dialer.
func NewDialer() *Dialer {
	return &Dialer{
		conns: lru.NewWithEvictionFunc(256, func(_ lru.Key, v interface{}) {
			if err := v.(*grpc.ClientConn).Close(); err != nil {
				slog.Warn("ateom dialer: failed to close evicted connection", slog.Any("err", err))
			}
		}),
	}
}

// DialPod returns a gRPC connection to the ateom running in the given pod,
// creating one if not already cached.
func (d *Dialer) DialPod(ctx context.Context, namespace, name string) (*grpc.ClientConn, error) {
	key := namespace + "/" + name
	if connAny, ok := d.conns.Get(key); ok {
		return connAny.(*grpc.ClientConn), nil
	}
	conn, err := grpc.NewClient(
		"unix://"+ateompath.AteomSocketPath(namespace, name),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("while dialing ateom pod %s/%s: %w", namespace, name, err)
	}
	d.conns.Add(key, conn)
	return conn, nil
}
