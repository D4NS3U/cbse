# Design note — Component reports and field-level ownership: cross-controller state in Kubernetes, and the CBSE SimulationExperiment

**Context:** architectural documentation for the CBSE framework; motivates and grounds the experiment-phase aggregation design (feature spec pending). The scenario-level `Finished` state and the evaluation loop it terminates are landed and cluster-proven (see the post-processing-service feature record); this note addresses the level above: the experiment's own phase.

## 1. The problem class

In any multi-controller system, a component holds knowledge about a resource whose owning controller is a different component: one component *knows the fact*, another *owns the object*. We call this the **component-report problem**. It is not an exotic integration problem; it is the standard condition of distributed control-plane architectures, and Kubernetes has an established, core-native answer for it.

## 2. The Kubernetes answer: field-level ownership

The governing mental model is:

> **Object ownership in Kubernetes is per-field, not per-object.** The spec belongs to the user; each slice of the status belongs to the component that knows the facts it expresses.

The "one controller per custom resource" idiom that controller-runtime encourages is a simplification for the long tail of the ecosystem — core Kubernetes itself departs from it constantly, because the real contract is: *one writer per field*. The report channel for a knowing component is therefore the **Status subresource** of the resource it reports about: the component writes its own, named slice; the owning controller derives its own fields from what others report.

## 3. Core precedents

Three core-Kubernetes objects make the pattern unmistakable:

- **Node.** The canonical multi-writer object: the kubelet owns the `Ready`, `MemoryPressure`, and `DiskPressure` conditions; the node-lifecycle controller owns its own conditions and eviction state; cloud providers have owned network-related conditions; further actors manage taints and labels. One object, many managers, disjoint fields, no chaos.
- **Pod.** Arguably the cleanest case: the **scheduler** writes the `PodScheduled` condition; the **kubelet** writes `Ready`, `containerStatuses`, and `podIP`. Two entirely different components writing one status object, each owning named slices.
- **Service.** The `status.loadBalancer` field is owned by cloud controllers — a component entirely separate from whoever manages the Service's spec.

A fourth pattern exists at the heavyweight end: **child-resource rollup** (Cluster API's architecture — children report via their *own* custom resources; parents derive status by watching them). It is the gold standard for large ecosystems, but it costs a resource type plus full controller plumbing per report; for an information payload of one word it is a scale mismatch.

## 4. The mechanics that make multi-writer status safe

The pattern is safe because the platform provides explicit rules:

1. **Field ownership** — convention, RBAC, and API documentation: each writer owns named fields and never writes another's. Server-Side Apply formalizes this with managed-field tracking; for disjoint simple fields, merge patches already cannot clobber each other.
2. **The status subresource** — status writes use a separate endpoint and verb set from spec writes, and crucially: *status updates do not increment `metadata.generation`*. A component's report therefore triggers the owner's watch (an event, a reconcile) **without simulating a spec change** — no reconcile loops, no false "the user changed the object" signals.
3. **Optimistic concurrency** — `resourceVersion` plus retry-on-conflict resolves the rare real collision.
4. **`observedGeneration`** — the standard staleness contract: a report always states which version of the object it was computed over, so consumers can distinguish fresh from stale reports.
5. **Patch disjointness** — merge patches that touch disjoint fields are inherently non-conflicting; shared or array-shaped fields (e.g., condition lists written by multiple components) are the case Server-Side Apply was built for.

## 5. Application to CBSE: the SimulationExperiment as a multi-writer object

CBSE has exactly this problem shape, by deliberate design:

- the **Scenario Manager** is the only component that knows scenario outcomes — they live in its Core Database, which no other component reads;
- the **Experiment Operator** owns the `SimulationExperiment` custom resource and its status, including the `phase` field it patches today.

The design this note grounds: the SimulationExperiment becomes a **multi-writer object in the sense of the Node** — the Scenario Manager owns one new, named status slice, and the operator derives its own `phase` from it:

```
status:
  scenarioAggregate:            # owned and written by the Scenario Manager
    verdict: Finished | Failed
    finished: <n>
    failed: <n>
    total: <n>
    observedGeneration: <generation the report was computed over>
  phase: ...                    # owned and written by the Experiment Operator,
                               # derived from scenarioAggregate via its existing
                               # patch path, with terminal stickiness
```

The mapping to the core precedent is direct: *the Scenario Manager is to the SimulationExperiment what the kubelet is to the Node* — the on-the-ground component that knows the ground truth, reporting into the object that a higher-level controller owns and derives from.

Two properties of the application domain make the design especially simple:

- **The reported condition is absorbing.** Scenario terminality is permanent (nothing leaves `Finished` or `Failed`), so the aggregate is computed once and is stable forever. Idempotent, at-least-once reports plus an idempotent derive are provably sufficient — no exactly-once machinery, no guard columns.
- **The report is monotone and typed.** The aggregate verdict plus component counts fit a small struct, giving the report first-class typing, API-documentation of ownership, and `kubectl` inspectability — rather than a stringly annotation.

### Design consequences

- The operator's reconcile gains one derive branch: read `scenarioAggregate`; if terminal, patch `phase` once; **terminal stickiness** — a terminal phase never regresses to `InProgress`.
- The `phase` becomes a derived cache of the report; any drift is self-healing on the next reconcile.
- The RBAC delta is one line: the Scenario Manager's role gains `patch` on `simulationexperiments/status`.
- The SM's report write is idempotent and hangs off its existing experiment informer.

## 6. The alternative channels, ranked

| Channel | Verdict | Rationale |
|---|---|---|
| **Status-subresource field** (§5) | **Canonical choice** | State reports belong in status: typed, inspectable, generation-tracked, precedented by Node/Pod/Service |
| Annotation handshake | Pragmatic cousin | Zero API/RBAC surface; precedented for *toggles and hints* (e.g., Cluster API's `cluster.x-k8s.io/paused`), but community convention reserves status for *state reports* |
| NATS message to the operator | Rejected | Foreign to the operator's architecture (pure controller-runtime); an entire consumer stack for a one-word payload |
| Child-CR rollup | Rejected here | The heavyweight canonical pattern; scale mismatch for one boolean |
| Kubernetes Events | Complementary only | Best-effort, human-facing observability garnish on the transition — never the carrier |

## 7. One-sentence summary

> Cross-component state about a resource is not a special integration problem; it is the standard condition of Kubernetes objects, solved by field-level ownership in the Status subresource — the kubelet does it to the Node, the scheduler does it to the Pod, and the Scenario Manager does it to the SimulationExperiment.
