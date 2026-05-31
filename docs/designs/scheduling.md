# Substrate Worker Pool Scheduling and Actor placement

Note: throughout this document, when we say *product*, we refer to systems that are built on top of Substrate.

This topic is discussed in [https://github.com/agent-substrate/substrate/issues/47](https://github.com/agent-substrate/substrate/issues/47).

## Critical User Journeys

- **Tiered service.** Two actors created from the same template must land on different worker pools depending on the user's subscription plan.

- **Application isolation.** Two actor types must not share workers. A worker that has run a browser actor should not be available to a code execution actor, whether for security, resource, or compliance reasons.

- **Zonal placement.** An actor communicates heavily with a zone-local backend (database, storage, downstream service). Resuming in a different zone incurs cross-zone egress charges and added latency. The actor must be pinned to the zone where its dependencies are deployed.

- **Pool blue-green rollout.** Worker pools need to be replaced: new ateom image, new node type, or configuration change. The new pool carries the same labels as the old one. The operator scales up the new pool and scales down the old one; actors migrate naturally as they suspend and resume. No actor-level changes required.

- **Customer-dedicated pools.** One tenant's actors must never share workers with another tenant's actors. The pool is provisioned per-tenant and must be unreachable to all other tenants.

- **Hardware requirements.** An actor template requires a specific hardware class (GPU for inference or vision workloads, high-memory nodes for large in-memory state). All actors from the template must land on workers with that hardware regardless of who creates them.

- **Hardware \+ tier.** An actor type always requires a GPU, but the specific GPU tier depends on the user's plan. The template enforces the GPU requirement; the per-actor selector further narrows to the right GPU class.

- **Application isolation \+ zone.** Two actor types each have isolated worker pools, and each must also be colocated with their respective zone-local backends. Both constraints must be enforced simultaneously via composition of the two selectors.

- **Worker Pod separation**. All worker pods, regardless of the actor template or actor selector, run on dedicated node pool(s). No other cluster workloads may run on those nodes. This is conceptually similar to [GKE’s workload separation](https://docs.cloud.google.com/kubernetes-engine/docs/how-to/workload-separation).

| \# | Use case | Doable today | Notes |
| :---- | :---- | :---- | :---- |
| 1 | Tiered service | With friction | Requires one `ActorTemplate` per tier (e.g. `coding-assistant-free`, `coding-assistant-paid`). Every config change (image update, runsc binary, snapshot location) must be applied to all N templates. Scales poorly beyond two tiers. |
| 2 | Application isolation | Yes | Each template already hard-references a single pool via `workerPoolRef`. Separate templates pointing to separate pools achieves isolation with no extra friction. |
| 3 | Zonal placement | With friction | Same template-per-zone duplication as tiered service. Additionally, `WorkerPool` has no `nodeSelector`, so pods cannot be pinned to a specific zone. |
| 4 | Pool blue-green rollout | No | Changing `workerPoolRef` on an `ActorTemplate` migrates all actors at once. There is no way to provision a replacement pool and gradually shift load by controlling replicas. |
| 5 | Customer-dedicated pools | With friction | Requires one `ActorTemplate` per tenant. Becomes unmanageable at scale (hundreds of customers \= hundreds of templates with identical container config). |
| 6 | Hardware requirements | No | `WorkerPoolSpec` has no `nodeSelector` or `tolerations`, so worker pods cannot be pinned to specific hardware. The template→pool binding via `workerPoolRef` works, but the pool cannot actually target GPU nodes. |
| 7 | Hardware \+ tier | With friction | Requires the cross-product of dimensions: `ml-sandbox-gpu-free`, `ml-sandbox-gpu-paid`, etc. Two dimensions with two values each \= four templates. Grows multiplicatively with each new dimension or value. |
| 8 | Application isolation \+ zone | With friction | Worse cross-product: two products × two zones \= four templates minimum, each needing independent maintenance. |
| 9 | Worker Pod Separation | No | WorkerPool’s spec has neither node selector nor tolerations, so worker pods land on whatever nodes k8s’ scheduler picks. |

## Proposed model

**Actor routing.** Worker pool selection uses two composable label selectors:

- `ActorTemplate.spec.workerSelector`: gates all actors from this template
- `Actor.worker_selector`: per-actor refinement within that gate

The scheduler ANDs both selectors against `WorkerPool` `metadata.labels` to find eligible pools, then picks a free worker from those pools. If no worker is found, `ResumeActor` fails.

An open question is whether worker pool lookup is cluster-wide or scoped to the k8s namespace where the WorkerPool / ActorTemplate lives. We expect WorkerPool and ActorTemplate to be managed by different personas, so we very likely want namespace segregation between them. If we make worker pools cluster-wide eligible, we will need some way for worker pool owners to restrict access to specific namespaces.

The actor's `worker_selector` is set at `CreateActor` and may only be updated via `UpdateActor` while the actor is `STATUS_SUSPENDED`.

**Worker pod placement.** `WorkerPoolSpec` currently only exposes `replicas` and `ateomImage`, so worker pods land on whatever nodes the scheduler picks. To support the use cases above, `WorkerPoolSpec` gains two fields that are passed through directly to the pod template spec: `nodeSelector` (pin to nodes with matching labels) and `tolerations` (allow scheduling onto tainted nodes, required for dedicated hardware such as GPU nodes).

The following pod placement fields are intentionally not exposed:

- `nodeName`: bypasses the scheduler and pins every pod to a single named node. With replicas \> 1 all workers pile onto one node. The same effect can be achieved with a unique `nodeSelector` label, which composes correctly with replicas.
- `nodeAffinity`: a more expressive superset of `nodeSelector` (preferred vs. required rules, set-based expressions). Not needed for the current use cases; can be added later as a purely additive change.
- `podAffinity` / `podAntiAffinity`: placement relative to other pods. No identified use case for worker pools at this time.
- Full `PodTemplateSpec`: would expose container definitions, volumes, and security contexts that the controller owns and must not be overridden by callers.
- `topologySpreadConstraints:`

In this proposal we assume ActorTemplates are immutable resources.

Let's go over all the aforementioned CUJs and give concrete examples:

## Tiered Service

Two actors from the same template must land on different pools. The template selector is empty; the actor selector is set at creation from the user's plan (`tier=free` or `tier=paid`) by the API layer.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: free-tier-workers
 namespace: platform
 labels:
   tier: free
spec:
 replicas: 80
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   cloud.google.com/machine-family: e2
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: paid-tier-workers
 namespace: platform
 labels:
   tier: paid
spec:
 replicas: 30
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   cloud.google.com/machine-family: c3
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: coding-assistant
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 containers:
   - name: agent
     image: gcr.io/my-project/coding-assistant:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/coding-assistant
```

**Substrate API: free-tier user**

```
CreateActorRequest:
 actor_id:                 "coding-agent-free-u103"
 actor_template_namespace: "platform"
 actor_template_name:      "coding-assistant"
 worker_selector:
   match_labels:
     tier: "free"

Actor (result):
 actor_id: "coding-agent-free-u103"
 status:   STATUS_SUSPENDED
 worker_selector:
   match_labels:
     tier: "free"
```

**Substrate API: paid-tier user**

```
CreateActorRequest:
 actor_id:                 "coding-agent-paid-u558"
 actor_template_namespace: "platform"
 actor_template_name:      "coding-assistant"
 worker_selector:
   match_labels:
     tier: "paid"

Actor (result):
 actor_id: "coding-agent-paid-u558"
 status:   STATUS_SUSPENDED
 worker_selector:
   match_labels:
     tier: "paid"
```

Scheduler: `template=(none), actor=tier=paid` → eligible: `[paid-tier-workers]`.

## Application Isolation

Two actor templates must not share workers. Each template's `workerSelector` pins it to its own pool; the pools are disjoint by label.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: code-sandbox-workers
 namespace: platform
 labels:
   workload: code-sandbox
spec:
 replicas: 30
 ateomImage: gcr.io/my-project/ateom:latest
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: browser-agent-workers
 namespace: platform
 labels:
   workload: browser-agent
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: code-sandbox
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 workerSelector:
   matchLabels:
     workload: code-sandbox
 containers:
   - name: sandbox
     image: gcr.io/my-project/code-sandbox:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/code-sandbox
```

**Substrate API**

```textproto
CreateActorRequest:
 actor_id:                 "sandbox-u847"
 actor_template_namespace: "platform"
 actor_template_name:      "code-sandbox"

Actor (result):
 actor_id:                 "sandbox-u847"
 actor_template_namespace: "platform"
 actor_template_name:      "code-sandbox"
 status:                   STATUS_SUSPENDED
```

Scheduler: `template=workload=code-sandbox, actor=(none)` → eligible: `[code-sandbox-workers]`. `browser-agent-workers` is unreachable regardless of load.

## Zonal Placement

The actor communicates with a zone-local backend (database, storage bucket). Resuming in a different zone incurs cross-zone egress charges. One worker pool per zone; the actor selector is set at creation to the zone where the backend is deployed.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: workers-us-central1-a
 namespace: platform
 labels:
   zone: us-central1-a
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-a
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: workers-us-central1-b
 namespace: platform
 labels:
   zone: us-central1-b
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-b
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: workers-us-central1-c
 namespace: platform
 labels:
   zone: us-central1-c
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-c
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: data-pipeline-actor
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 containers:
   - name: pipeline
     image: gcr.io/my-project/data-pipeline:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/data-pipeline
```

**Substrate API**

```
CreateActorRequest:
 actor_id:                 "pipeline-u991"
 actor_template_namespace: "platform"
 actor_template_name:      "data-pipeline-actor"
 worker_selector:
   match_labels:
     zone: "us-central1-a"   # zone where the actor's database replica lives

Actor (result):
 actor_id: "pipeline-u991"
 status:   STATUS_SUSPENDED
 worker_selector:
   match_labels:
     zone: "us-central1-a"
```

Scheduler: `template=(none), actor=zone=us-central1-a` → eligible: `[workers-us-central1-a]`. All traffic between the actor and its database stays within the zone; no egress charges.

## Pool Blue-Green Rollout

The new pool carries the same labels as the old one. The operator scales up the new pool and scales down the old one. As actors suspend and resume naturally, the scheduler picks free workers from whichever matching pools have capacity. No actor-level changes are needed; migration is entirely a pool management operation.

**WorkerPools**

```
# Existing pool. Scale down replicas to drain.
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: agent-workers-old
 namespace: platform
 labels:
   workload: agent-harness      # same labels as the new pool
spec:
 replicas: 40                   # reduce gradually to 0 as migration proceeds
 ateomImage: gcr.io/my-project/ateom:v1
 nodeSelector:
   cloud.google.com/machine-family: n2
---
# New pool. Scale up replicas to absorb load.
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: agent-workers-new
 namespace: platform
 labels:
   workload: agent-harness      # identical labels; both pools are eligible
spec:
 replicas: 40                   # increase as old pool drains
 ateomImage: gcr.io/my-project/ateom:v2
 nodeSelector:
   cloud.google.com/machine-family: c3
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: agent-harness
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 workerSelector:
   matchLabels:
     workload: agent-harness
 containers:
   - name: harness
     image: gcr.io/my-project/agent-harness:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/agent-harness
```

**Substrate API**

```
ResumeActorRequest:
 actor_id: "harness-u631"

Actor (result):
 actor_id:       "harness-u631"
 status:         STATUS_RUNNING
 ateom_pod_name: "agent-workers-new-deployment-9b2e1a-mkp4r"
```

Scheduler: `template=workload=agent-harness, actor=(none)` → eligible: `[agent-workers-old, agent-workers-new]`. As `agent-workers-old` is scaled down, its workers are no longer free; resumes naturally land on `agent-workers-new`. No `UpdateActor` calls, no product-layer involvement.

## Customer-Dedicated Pools

Actors from one tenant must not share workers with actors from another tenant. Each tenant has a dedicated pool labeled with their tenant ID. The actor selector is set at creation and must be unreachable to other tenants.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: acme-corp-workers
 namespace: platform
 labels:
   tenant: acme-corp
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   tenant: acme-corp
 tolerations:
   - key: tenant
     value: acme-corp
     effect: NoSchedule
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: shared-workers
 namespace: platform
 labels:
   tenant: shared
spec:
 replicas: 100
 ateomImage: gcr.io/my-project/ateom:latest
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: research-agent
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 containers:
   - name: agent
     image: gcr.io/my-project/research-agent:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/research-agent
```

**Substrate API**

```
CreateActorRequest:
 actor_id:                 "research-agent-acme-t912"
 actor_template_namespace: "platform"
 actor_template_name:      "research-agent"
 worker_selector:
   match_labels:
     tenant: "acme-corp"

Actor (result):
 actor_id: "research-agent-acme-t912"
 status:   STATUS_SUSPENDED
 worker_selector:
   match_labels:
     tenant: "acme-corp"
```

Scheduler: `template=(none), actor=tenant=acme-corp` → eligible: `[acme-corp-workers]`. Shared-tenant actors use `tenant=shared`. `acme-corp-workers` are unreachable to them.

## Hardware Requirements

The actor template requires GPU workers. The `workerSelector` on the template enforces this; the actor selector is unused.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: gpu-workers
 namespace: platform
 labels:
   resource: gpu
spec:
 replicas: 10
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   cloud.google.com/gke-accelerator: nvidia-l4
 tolerations:
   - key: nvidia.com/gpu
     operator: Exists
     effect: NoSchedule
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: cpu-workers
 namespace: platform
 labels:
   resource: cpu
spec:
 replicas: 40
 ateomImage: gcr.io/my-project/ateom:latest
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: vision-agent
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 workerSelector:
   matchLabels:
     resource: gpu
 containers:
   - name: agent
     image: gcr.io/my-project/vision-agent:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/vision-agent
 runsc:
   amd64:
     sha256Hash: "abc123..."
     url: gs://my-bucket/runsc/amd64
```

**Substrate API**

```
CreateActorRequest:
 actor_id:                 "vision-agent-u291"
 actor_template_namespace: "platform"
 actor_template_name:      "vision-agent"

Actor (result):
 actor_id:                 "vision-agent-u291"
 actor_template_namespace: "platform"
 actor_template_name:      "vision-agent"
 status:                   STATUS_SUSPENDED

```

Scheduler: `template=resource=gpu, actor=(none)` → eligible: `[gpu-workers]`. `cpu-workers` is never reachable from this template.

## Hardware \+ Tier

The template always requires GPU (`resource=gpu` in the template selector). Within the GPU pool set, the actor selector further narrows by tier (`tier=free` maps to T4 pools, `tier=paid` to H100 pools). CPU pools are unreachable regardless of the actor selector.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: gpu-free-workers
 namespace: platform
 labels:
   resource: gpu
   tier: free
spec:
 replicas: 20
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   cloud.google.com/gke-accelerator: nvidia-t4
 tolerations:
   - key: nvidia.com/gpu
     operator: Exists
     effect: NoSchedule
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: gpu-paid-workers
 namespace: platform
 labels:
   resource: gpu
   tier: paid
spec:
 replicas: 10
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   cloud.google.com/gke-accelerator: nvidia-h100
 tolerations:
   - key: nvidia.com/gpu
     operator: Exists
     effect: NoSchedule
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: cpu-workers
 namespace: platform
 labels:
   resource: cpu
spec:
 replicas: 60
 ateomImage: gcr.io/my-project/ateom:latest
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: ml-sandbox
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 workerSelector:
   matchLabels:
     resource: gpu
 containers:
   - name: sandbox
     image: gcr.io/my-project/ml-sandbox:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/ml-sandbox
```

**Substrate API**

```
CreateActorRequest:
 actor_id:                 "ml-sandbox-paid-u319"
 actor_template_namespace: "platform"
 actor_template_name:      "ml-sandbox"
 worker_selector:
   match_labels:
     tier: "paid"

Actor (result):
 actor_id: "ml-sandbox-paid-u319"
 status:   STATUS_SUSPENDED
 worker_selector:
   match_labels:
     tier: "paid"
```

Scheduler: `template=resource=gpu, actor=tier=paid` → eligible: `[gpu-paid-workers]`. `gpu-free-workers` excluded (tier mismatch). `cpu-workers` excluded (resource mismatch, regardless of tier).

## Application Isolation \+ Zone

Two actor types have isolated pool sets (template selector), and each must also be pinned to a specific zone to avoid cross-zone egress (actor selector). Both constraints are ANDed: only a pool matching both workload and zone is eligible.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: coding-actor-zone-a-workers
 namespace: platform
 labels:
   workload: coding-actor
   zone: us-central1-a
spec:
 replicas: 15
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-a
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: coding-actor-zone-b-workers
 namespace: platform
 labels:
   workload: coding-actor
   zone: us-central1-b
spec:
 replicas: 15
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-b
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: browser-actor-zone-a-workers
 namespace: platform
 labels:
   workload: browser-actor
   zone: us-central1-a
spec:
 replicas: 10
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   topology.kubernetes.io/zone: us-central1-a
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: coding-actor
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 workerSelector:
   matchLabels:
     workload: coding-actor
 containers:
   - name: actor
     image: gcr.io/my-project/coding-actor:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/coding-actor
```

**Substrate API**

```textproto
# CreateActorRequest
actor_id: "coding-actor-u482"
actor_template_namespace: "platform"
actor_template_name: "coding-actor"
worker_selector {
 match_labels { key: "zone" value: "us-central1-a" }
}

# Actor
actor_id: "coding-actor-u482"
status: STATUS_SUSPENDED
worker_selector {
 match_labels { key: "zone" value: "us-central1-a" }
}
```

Scheduler: `template=workload=coding-actor, actor=zone=us-central1-a` → eligible: `[coding-actor-zone-a-workers]`. `coding-actor-zone-b-workers` excluded (zone mismatch). `browser-actor-zone-a-workers` excluded (workload mismatch).

## Worker Pod Separation

All worker pods must run on a dedicated node pool regardless of actor template or actor selector. This is a cluster-level placement constraint — no other workloads may run on those nodes. The `ActorTemplate` and actor `worker_selector` play no role; this is enforced entirely through `WorkerPool` configuration and node-level taints.

The node pool is tainted with `NoSchedule` to repel all other cluster workloads. Each `WorkerPool` opts in via `tolerations` and is pinned to those nodes via `nodeSelector`. Together, the taint prevents non-worker pods from landing on worker nodes, and the `nodeSelector` prevents worker pods from spilling onto other nodes.

**WorkerPools**

```
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: general-workers
 namespace: platform
 labels:
   workload: general
spec:
 replicas: 40
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   node-pool: substrate-workers
 tolerations:
   - key: substrate.dev/worker
     operator: Exists
     effect: NoSchedule
---
apiVersion: ate.dev/v1alpha1
kind: WorkerPool
metadata:
 name: gpu-workers
 namespace: platform
 labels:
   resource: gpu
spec:
 replicas: 10
 ateomImage: gcr.io/my-project/ateom:latest
 nodeSelector:
   node-pool: substrate-workers
   cloud.google.com/gke-accelerator: nvidia-l4
 tolerations:
   - key: substrate.dev/worker
     operator: Exists
     effect: NoSchedule
   - key: nvidia.com/gpu
     operator: Exists
     effect: NoSchedule
```

**ActorTemplate**

```
apiVersion: ate.dev/v1alpha1
kind: ActorTemplate
metadata:
 name: my-actor
 namespace: platform
spec:
 pauseImage: registry.k8s.io/pause:3.10.2
 containers:
   - name: agent
     image: gcr.io/my-project/my-actor:latest
 snapshotsConfig:
   location: gs://my-bucket/snapshots/my-actor
```

**Substrate API**

```textproto
# CreateActorRequest
actor_id: "my-actor-u123"
actor_template_namespace: "platform"
actor_template_name: "my-actor"
```

## API changes

### CRD changes

#### WorkerPool

**Remove**

- `spec.workerPoolRef` on `ActorTemplate` (see below). No changes to `WorkerPool` identity or structure.

**Add to `WorkerPoolSpec`**

```go
// NodeSelector pins worker pods to nodes whose labels match all entries.
// +optional
NodeSelector map[string]string `json:"nodeSelector,omitempty"`

// Tolerations allow worker pods to be scheduled onto nodes with matching
// taints. Required for dedicated or specialized node pools (e.g. GPU nodes)
// that carry a NoSchedule taint to repel ordinary workloads.
// +optional
Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
```

Both fields are passed through unchanged to the pod template spec in `buildDeploymentApplyConfig`. Fields not set by the caller are omitted from the apply configuration, preserving current behavior for existing pools.

`nodeAffinity`, `podAffinity`/`podAntiAffinity`, `topologySpreadConstraints` and `nodeName` are intentionally omitted, although
they could be additive changes in the future if we find good use cases. Worker pods *are* k8s pods, and subject to
k8s scheduling logic.

#### ActorTemplate

**Remove**

```go
// Removed: single hard reference to one pool.
WorkerPoolRef corev1.ObjectReference `json:"workerPoolRef"`
```

**Add**

```go
// WorkerSelector restricts which worker pools actors from this template may
// use. The scheduler only considers pools whose labels match this selector.
// If nil, all pools are eligible (subject to the actor's own worker_selector).
// Acts as a gate: the actor's worker_selector can only narrow this set further,
// never expand it.
// +optional
WorkerSelector *metav1.LabelSelector `json:"workerSelector,omitempty"`
```

The hard `workerPoolRef` reference is replaced by a label selector evaluated at resume time against live `WorkerPool` objects. This allows one template to target multiple pools (enabling tiered service, regional placement, and migration) without duplicating container configuration across templates. No backward compatibility is provided for `workerPoolRef`.

---

### Substrate API changes

#### New `Selector` message

```protobuf
// Selector matches worker pools by label. Only equality-based matching is
// supported for now. match_expressions may be added in a future revision
// without breaking this message.
message Selector {
 map<string, string> match_labels = 1;
}
```

A dedicated message rather than a raw `map<string, string>` so that `match_expressions` support can be added later without a breaking API change.

#### `Actor` message

**Add**

```protobuf
// worker_selector is the per-actor placement constraint. The scheduler
// evaluates the AND of this selector and the template's workerSelector to
// find eligible pools. Set at CreateActor; may only be updated via
// UpdateActor while the actor is STATUS_SUSPENDED.
Selector worker_selector = 11;
```

Stores the actor's placement intent durably so that every resume uses the same selector without the caller having to re-supply it. Immutability while running prevents placement from shifting under a live actor.

#### `CreateActorRequest`

**Add**

```protobuf
// worker_selector sets the actor's placement constraint at creation time.
// If empty, the actor matches any pool admitted by the template's selector.
Selector worker_selector = 4;
```

The caller supplies the actor's placement context at creation, typically derived from the user's plan, account region, or tenant config. The value is stored on the actor and used for all subsequent resumes.

#### New `UpdateActor` RPC

```protobuf
rpc UpdateActor(UpdateActorRequest) returns (UpdateActorResponse) {}

// UpdateActorRequest allows updating mutable actor fields.
// The actor must be STATUS_SUSPENDED; the RPC returns FAILED_PRECONDITION
// if the actor is in any other state.
message UpdateActorRequest {
 string actor_id = 1;

 // worker_selector replaces the actor's current placement constraint.
 // Takes effect on the next ResumeActor call.
 Selector worker_selector = 2;
}

message UpdateActorResponse {
 Actor actor = 1;
}
```

Required when an actor's placement properties change after creation: a user upgrades from free to paid tier, an actor is reassigned to a different zone, or a tenant pool transfer occurs. Restricting updates to `STATUS_SUSPENDED` ensures the actor is not mid-flight when its placement changes; the new selector takes effect cleanly on the next resume.
