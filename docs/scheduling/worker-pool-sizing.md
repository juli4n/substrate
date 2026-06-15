# Worker Pool Sizing

> This document builds on [worker-pool-selection.md](worker-pool-selection.md). The label-based worker pool selection mechanism described there is a prerequisite; the size class proposal here adds a resource dimension on top of it.

## Critical User Journeys

- **Resource guarantee.** An actor template requires predictable CPU and memory. Worker pods must have resource requests set so k8s can schedule them onto appropriately sized nodes and they are not first in line for eviction under memory pressure.

- **Tiered service by size.** Two actors from the same template must land on workers with different resource profiles depending on the user's plan. A free-tier actor gets a small pod; a paid-tier actor gets a large pod. No separate pools or templates per tier.

- **Size upgrade.** A user upgrades from free to paid. The actor's size changes without suspending it first; the new size takes effect on the next resume.

| \# | Use case | Doable today | Notes |
| :---- | :---- | :---- | :---- |
| 1 | Resource guarantee | No | `WorkerPoolSpec` has no resource fields; all pods are `BestEffort` |
| 2 | Tiered service by size | With friction | Requires one pool per size class per workload type: N×M pools for N workload types and M size classes |
| 3 | Size upgrade | No | No `worker_size` field exists; no `UpdateActor` RPC |

## Background

`WorkerPoolSpec` currently exposes only `replicas` and `ateomImage`. The controller creates a Deployment whose pod template sets no `resources.requests`. Kubernetes assigns these pods the `BestEffort` QoS class, which means they are the first candidates for eviction when a node runs low on memory. Under load, worker pods, along with the actors running inside them, can be killed without notice.

There is no field on `ActorTemplate` or `Actor` to declare how much CPU or memory an actor workload needs.

The label-based `workerSelector` mechanism (see [worker-pool-selection.md](worker-pool-selection.md)) handles pool selection, but if resource sizing is modeled as just another label dimension, the number of pools multiplies.

The workload type and the size class are logically orthogonal, so conflating them into a single label forces operators to manage their cross product.

## Proposal

### Size classes on WorkerPool

`WorkerPoolSpec` gains a `sizeClasses` list. Each entry is a named capacity profile with its own replica count and resource requirements. For now, the controller creates one Deployment per size class. Eventually, we can replace this with our own controller (e.g. to be smarter when deciding which pod should be evicted during scale down).

```yaml
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
  name: coding-actor-workers
  namespace: platform
  labels:
    workload: coding-actor
spec:
  ateomImage: gcr.io/my-project/ateom:latest
  nodeSelector:
    cloud.google.com/machine-family: n2
  sizeClasses:
    - name: small
      replicas: 100
      resources:
        requests:
          cpu: 500m
          memory: 2Gi
    - name: large
      replicas: 10
      resources:
        requests:
          cpu: 8
          memory: 32Gi
```

### workerSize on ActorTemplate and Actor

`ActorTemplate` gains an optional `workerSize` field. The template author declares the resource profile their workload needs. All actors created from the template inherit this size by default. If unset, the scheduler picks any free worker across all size classes in the eligible pools.

```yaml
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
  name: coding-assistant
  namespace: platform
spec:
  workerSelector:
    matchLabels:
      workload: coding-actor
  workerSize: small
  containers:
    - name: agent
      image: gcr.io/my-project/coding-assistant:latest
  snapshotsConfig:
    location: gs://my-bucket/snapshots/coding-assistant
```

`CreateActorRequest` and `UpdateActorRequest` accept an optional `worker_size` that overrides the template default. This covers cases where a specific actor needs a different capacity class than the template default; for example, a paid user getting a `large` worker while the template defaults to `small`.

```textproto
# CreateActorRequest
actor_id: "coding-agent-paid-u558"
actor_template_namespace: "platform"
actor_template_name: "coding-assistant"
worker_size: "large"  # overrides template default of "small"
```

The semantics here are override, not AND: the actor replaces the template's `workerSize`, not intersects with it. Size is a resource preference rather than an isolation constraint, so there is no security reason to prevent the actor from requesting a larger class.

`worker_size` may be updated at any time via `UpdateActor`, regardless of the actor's current status. If the actor is running, the update is stored immediately but does not affect the current session; the actor continues on its existing worker. The new size takes effect on the next `ResumeActor` call.

## Resource Guarantee

An actor template requires consistent memory and CPU to run correctly. Without resource requests, worker pods have `BestEffort` QoS and are evicted first under node pressure. A single size class with explicit requests moves the pool to `Burstable` QoS and gives k8s the information it needs to schedule pods onto nodes with sufficient capacity.

**WorkerPool**

```yaml
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
  name: agent-sandbox-workers
  namespace: platform
  labels:
    workload: agent-sandbox
spec:
  ateomImage: gcr.io/my-project/ateom:latest
  sizeClasses:
    - name: standard
      replicas: 40
      resources:
        requests:
          cpu: "2"
          memory: 8Gi
        limits:
          memory: 8Gi
```

**ActorTemplate**

```yaml
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
  name: agent-sandbox
  namespace: platform
spec:
  workerSelector:
    matchLabels:
      workload: agent-sandbox
  workerSize: standard
  containers:
    - name: sandbox
      image: gcr.io/my-project/agent-sandbox:latest
  snapshotsConfig:
    location: gs://my-bucket/snapshots/agent-sandbox
```

**Substrate API**

```textproto
# CreateActorRequest
actor_id: "sandbox-u991"
actor_template_namespace: "platform"
actor_template_name: "agent-sandbox"

# Actor
actor_id: "sandbox-u991"
status: STATUS_SUSPENDED
worker_size: "standard"
```

Scheduler: `workerSelector=workload=agent-sandbox, workerSize=standard` → eligible workers are those from the `agent-sandbox-workers-standard` Deployment. Each pod has `requests.cpu=2, requests.memory=8Gi, limits.memory=8Gi`, giving `Burstable` QoS.

## Tiered Service by Size

Two actors from the same template land on workers with different resource profiles based on the user's plan. A single pool carries both size classes; no separate pools or templates are needed per tier.

**WorkerPool**

```yaml
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
  name: agent-sandbox-workers
  namespace: platform
  labels:
    workload: agent-sandbox
spec:
  ateomImage: gcr.io/my-project/ateom:latest
  nodeSelector:
    cloud.google.com/machine-family: n2
  sizeClasses:
    - name: small
      replicas: 100
      resources:
        requests:
          cpu: 500m
          memory: 2Gi
        limits:
          memory: 2Gi
    - name: large
      replicas: 20
      resources:
        requests:
          cpu: "4"
          memory: 16Gi
        limits:
          memory: 16Gi
```

**ActorTemplate**

```yaml
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
  name: agent-sandbox
  namespace: platform
spec:
  workerSelector:
    matchLabels:
      workload: agent-sandbox
  workerSize: small
  containers:
    - name: sandbox
      image: gcr.io/my-project/agent-sandbox:latest
  snapshotsConfig:
    location: gs://my-bucket/snapshots/agent-sandbox
```

**Substrate API: free-tier user**

```textproto
# CreateActorRequest
actor_id: "sandbox-free-u103"
actor_template_namespace: "platform"
actor_template_name: "agent-sandbox"

# Actor
actor_id: "sandbox-free-u103"
status: STATUS_SUSPENDED
worker_size: "small"
```

**Substrate API: paid-tier user**

```textproto
# CreateActorRequest
actor_id: "sandbox-paid-u558"
actor_template_namespace: "platform"
actor_template_name: "agent-sandbox"
worker_size: "large"

# Actor
actor_id: "sandbox-paid-u558"
status: STATUS_SUSPENDED
worker_size: "large"
```

Scheduler: free actor → `workerSize=small` → `agent-sandbox-workers-small` Deployment. Paid actor → `workerSize=large` → `agent-sandbox-workers-large` Deployment. One pool, one template, two resource profiles.

## Autoscaling

Worker pool autoscaling is proposed in [issue #198](https://github.com/agent-substrate/substrate/issues/198) and implemented in [PR #219](https://github.com/agent-substrate/substrate/pull/219). Each size class is an independently autoscaled unit; the autoscaler is expected to dynamically rightsize the replica count for each size class based on its own utilization.

## Size Upgrade

A user upgrades from free to paid. The product API layer calls `UpdateActor` to change the actor's size. The actor may be running at the time; the update is stored immediately and takes effect on the next resume. No suspension is required.

**Substrate API**

```textproto
# UpdateActorRequest: called when user upgrades plan
actor_id: "sandbox-free-u103"
worker_size: "large"

# Actor (response): actor may still be STATUS_RUNNING
actor_id: "sandbox-free-u103"
status: STATUS_RUNNING
worker_size: "large"          # stored; not yet in effect
```

On the next `ResumeActor` call, after the actor naturally suspends, the scheduler uses `workerSize=large` and assigns a worker from the `agent-sandbox-workers-large` Deployment. No product-layer involvement at resume time.
