# Design decisions

## Problem statement

In a multi-tenant cluster, a namespace is an operational boundary but Kubernetes does not provide a native monthly cost budget. Resource requests, quotas, and limits constrain capacity; they do not answer how much CPU or memory a tenant has consumed over a billing period. Without a per-namespace control loop, a noisy workload can consume shared capacity and leave platform teams with delayed, aggregate billing data rather than an actionable response.

`namespace-cost-governor` makes the namespace the unit of governance. A platform administrator declares a `NamespaceBudget`; the operator measures usage from Prometheus, records a month-to-date total, and applies explicitly configured actions as usage crosses thresholds.

## Why an operator

An operator is appropriate because the desired policy is a Kubernetes object and enforcement requires both observation and mutation. The reconciler can watch the budget, query an external metrics API, update status, patch workloads, create reports, and retry after transient failures. Kubernetes status and conditions expose the result to other automation.

Kyverno and OPA are useful for admission-time policy. They can reject or mutate an object at creation or update time, but they do not naturally accumulate time-series usage or perform a monthly accounting cycle. Kubecost provides a richer cost-allocation and billing view, but it is an observability and allocation system rather than this project's deliberately small, namespace-scoped enforcement loop. The operator can consume Prometheus data without requiring Kubecost and can take the policy action immediately. These tools can coexist: admission policy can protect object shape, Kubecost can provide detailed billing, and this operator can enforce a declared budget.

## Reconciliation loop

Each `NamespaceBudget` is reconciled approximately every 60 seconds. The operator queries Prometheus for current per-pod CPU and memory usage, calculates the interval since the previous observation, and adds that interval's usage to `status.accumulatedUsage`. It also updates current usage, conditions, phase, and idle workload information.

The operator accumulates incrementally rather than querying the entire month on every tick. This bounds query size and latency, avoids repeatedly processing a growing range, and preserves a stable accounting checkpoint in Kubernetes status. The trade-off is that a missed or delayed reconciliation interval is not reconstructed from historical samples; the result depends on the sampling interval and Prometheus availability. The accumulation resets at the start of a new month after the previous period is reported.

## Annotation-first scale-down

Before changing a Deployment's replica count, `ScaleDown` writes the operator annotations containing the original replica count, timestamp, actor, and reason. It then patches replicas to zero. This ordering makes the recovery record durable before the destructive mutation. If the process crashes after the annotation patch but before the replica patch, a later reconciliation can complete the operation. If replicas were patched first, a crash could leave a zero-scaled workload without enough information to restore it.

The operator only acts on Deployments and skips workloads already marked as scaled down by the operator. Exclusions are checked before every action.

## Two-patch pattern

Scale-down and restore use separate patches: one patch for annotations and one patch for `spec.replicas`. Restore reads the saved original replica count, patches replicas, then removes the operator annotations. Ordering restore this way means a crash after the replica patch leaves the recovery metadata available for a retry; removing the metadata first would make the original count unrecoverable.

The patches are intentionally separate because annotations and replicas have different recovery semantics and because each update can conflict independently with another controller. Reconciliation retries on API errors.

## Phase calculation

CPU and memory are independent budget dimensions. The operator computes each percentage against its corresponding monthly budget and sets:

$$
\text{budgetPercent} = \max(\text{cpuPercent},\ \text{memoryPercent})
$$

A phase is therefore driven by the dimension that is closest to exhaustion. Averaging would hide a breach: for example, 140% CPU and 20% memory would average to 80%, incorrectly avoiding the hard response despite CPU being over the limit.

## Notifications

Slack notifications are emitted only when the phase changes. A reconcile loop runs every minute; sending the same notification on every tick would create a notification storm and make a transition difficult to identify. Condition transition timestamps follow the same rule: `LastTransitionTime` changes only when the condition status changes, not when usage is merely refreshed.

## Monthly reports and lifecycle

On the first day of a month, the operator checks for the previous period's `CostReport` before creating it. The existence check makes report creation idempotent across retries and leader changes. A finalizer named `cost.platform.io/cleanup` gives the controller an explicit deletion hook for cleanup of operator-managed state before the `NamespaceBudget` is removed.

## Known limitations and trade-offs

- Prometheus is the source of truth. Missing, delayed, or incorrectly labelled metrics reduce accounting accuracy.
- Incremental accounting does not backfill periods during which the operator was unavailable.
- The model measures CPU and working-set memory, not provider billing dimensions such as storage, network egress, reservations, or spot pricing.
- Idle detection is CPU-based and operates on Deployment workloads. Other workload kinds are ignored rather than inferred.
- Scaling a Deployment to zero is disruptive and can race with another controller or a user changing replicas.
- Restoration depends on the operator annotations remaining intact and on the Deployment still existing.
- Slack delivery is best effort; an HTTP failure is observable as an action error but is not a durable message queue.
- The current CRD has no user-configurable grace period, approval workflow, or per-workload budget.
