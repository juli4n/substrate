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

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/agent-substrate/substrate/cmd/atelet/internal/workerstore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const workerPoolLabel = "ate.dev/worker-pool"

// NewWorkerPodInformer creates a pod informer scoped to pods on nodeName that
// belong to a worker pool. The caller is responsible for starting the factory
// and waiting for the cache to sync before using the informer.
func NewWorkerPodInformer(kc kubernetes.Interface, nodeName string) (informers.SharedInformerFactory, cache.SharedIndexInformer) {
	factory := informers.NewSharedInformerFactoryWithOptions(kc, 0,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = workerPoolLabel
			opts.FieldSelector = "spec.nodeName=" + nodeName
		}),
	)
	return factory, factory.Core().V1().Pods().Informer()
}

// WorkerPoolSyncer keeps the WorkerStore in sync with the live set of worker
// pods. It combines edge-triggered updates (informer events) with a periodic
// level-triggered reconcile to correct any drift from missed events.
// The informer must already be synced before Run is called.
type WorkerPoolSyncer struct {
	store    *workerstore.Store
	informer cache.SharedIndexInformer
}

// NewWorkerPoolSyncer creates a syncer backed by an already-synced informer.
func NewWorkerPoolSyncer(informer cache.SharedIndexInformer, store *workerstore.Store) *WorkerPoolSyncer {
	return &WorkerPoolSyncer{store: store, informer: informer}
}

// Run registers event handlers, runs an immediate reconcile against the
// already-warm cache, then periodically reconciles until ctx is cancelled.
// Returns an error if the informer cache is not yet synced.
func (s *WorkerPoolSyncer) Run(ctx context.Context) error {
	if !s.informer.HasSynced() {
		return fmt.Errorf("informer cache not synced; call WaitForCacheSync before Run")
	}
	s.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			s.syncPod(ctx, pod)
		},
		UpdateFunc: func(_, newObj interface{}) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			s.syncPod(ctx, pod)
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
					slog.ErrorContext(ctx, "worker informer: unexpected type in delete event")
					return
				}
			default:
				slog.ErrorContext(ctx, "worker informer: unexpected type in delete event")
				return
			}
			if err := s.store.UnregisterWorkerPod(pod.UID); err != nil {
				slog.ErrorContext(ctx, "worker informer: failed to unregister pod", slog.String("pod", pod.Name), slog.Any("err", err))
			}
		},
	})

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.reconcile(ctx)
		}
	}
}

func (s *WorkerPoolSyncer) reconcile(ctx context.Context) {
	allWorkerPods := s.informer.GetIndexer().List()
	liveUIDs := make(map[workerstore.WorkerPodUID]struct{}, len(allWorkerPods))
	for _, obj := range allWorkerPods {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		liveUIDs[pod.UID] = struct{}{}
		s.syncPod(ctx, pod)
	}

	workerPodUIDs, err := s.store.WorkerPodUIDs()
	if err != nil {
		slog.ErrorContext(ctx, "worker reconcile: failed to list pod UIDs", slog.Any("err", err))
		return
	}
	for _, workerPodUID := range workerPodUIDs {
		if _, live := liveUIDs[workerPodUID]; !live {
			if err := s.store.UnregisterWorkerPod(workerPodUID); err != nil {
				slog.ErrorContext(ctx, "worker reconcile: failed to unregister stale pod", slog.String("uid", string(workerPodUID)), slog.Any("err", err))
				continue
			}
			slog.InfoContext(ctx, "worker reconcile: removed stale pod record", slog.String("uid", string(workerPodUID)))
		}
	}
}

func (s *WorkerPoolSyncer) syncPod(ctx context.Context, pod *corev1.Pod) {
	if pod.Status.PodIP == "" {
		return
	}
	if pod.DeletionTimestamp != nil {
		if err := s.store.UnregisterWorkerPod(pod.UID); err != nil {
			slog.ErrorContext(ctx, "worker informer: failed to unregister deleting pod", slog.String("pod", pod.Name), slog.Any("err", err))
		}
		return
	}
	podName := workerstore.WorkerPodName{Namespace: pod.Namespace, Name: pod.Name}
	poolName := workerstore.WorkerPoolName{Namespace: pod.Namespace, Name: pod.Labels[workerPoolLabel]}
	if err := s.store.RegisterWorkerPod(pod.UID, podName, pod.Status.PodIP, poolName); err != nil {
		slog.ErrorContext(ctx, "worker informer: failed to register pod", slog.String("pod", pod.Name), slog.Any("err", err))
	}
}
