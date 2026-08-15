# API reference

API group: `cost.cost.platform.io/v1alpha1`. `NamespaceBudget` and `CostReport` are namespaced resources. Unless stated otherwise, the Go type is the type shown by the CRD schema. No defaulting webhook is defined for the fields below; values marked “none” have no API default.

## NamespaceBudget

### Spec

| Field | Type | Required | Default | Description |
|---|---|---:|---|---|
| `spec.monthly` | `Usage` object | yes | none | Monthly CPU and memory budget. |
| `spec.monthly.cpu` | string | yes | none | CPU budget in core-hours for the month. |
| `spec.monthly.memory` | string | yes | none | Memory budget in GiB-hours for the month. |
| `spec.idleThreshold` | `IdleThreshold` object | yes | none | CPU and time-window criteria used to identify idle Deployments. |
| `spec.idleThreshold.cpuPercent` | integer | yes | none | A workload below this percentage of requested CPU is eligible as idle. |
| `spec.idleThreshold.window` | string | yes | none | Prometheus averaging window, for example `30m`. |
| `spec.actions` | `Actions` object | yes | none | Actions evaluated for warning, exceeded, and hard-limit phases. |
| `spec.actions.onWarning` | array of `Action` | no | empty | Actions run when usage enters the warning phase. |
| `spec.actions.onExceeded` | array of `Action` | no | empty | Actions run when usage enters the exceeded phase. |
| `spec.actions.onHardLimit` | array of `Action` | no | empty | Actions run when usage enters the suspended phase. |
| `spec.actions.*[].notify` | string | no | empty | Notification target; `slack` selects the configured Slack webhook. |
| `spec.actions.*[].scaleDownIdle` | boolean | no | `false` | Scale idle Deployments to zero. |
| `spec.actions.*[].suspendAll` | boolean | no | `false` | Scale all non-excluded Deployments in the namespace to zero. |
| `spec.exclusions` | array of `Exclusion` | no | empty | Workloads excluded from all operator actions. |
| `spec.exclusions[].name` | string | yes | none | Exact workload name to exclude. |
| `spec.exclusions[].labelSelector` | `metav1.LabelSelector` object | no | none | Set-based label selector for excluded workloads. |

`spec.actions.*[]` means an item in any of the three action arrays. An action can contain more than one enabled field. An exclusion is matched by exact name or by its label selector; excluded workloads are never scaled down or suspended.

### Status

| Field | Type | Required | Default | Description |
|---|---|---:|---|---|
| `status.phase` | string | no | empty | Current phase: `OK`, `Warning`, `Exceeded`, or `Suspended`. |
| `status.currentUsage` | `Usage` object | no | empty | Most recent usage observation for CPU and memory. |
| `status.currentUsage.cpu` | string | no | empty | Latest instantaneous CPU usage in cores. |
| `status.currentUsage.memory` | string | no | empty | Latest instantaneous working-set memory usage in GiB. |
| `status.accumulatedUsage` | `AccumulatedUsage` object | no | empty | Month-to-date usage accumulated across reconciliation ticks. |
| `status.accumulatedUsage.coreHours` | string | no | empty | Accumulated CPU consumption in core-hours. |
| `status.accumulatedUsage.gibHours` | string | no | empty | Accumulated memory consumption in GiB-hours. |
| `status.accumulatedUsage.since` | string | no | empty | RFC3339 timestamp from which the current accumulation began. |
| `status.lastReconcile` | `metav1.Time` | no | zero time | Time of the latest completed reconciliation status update. |
| `status.budgetPercent` | integer | no | `0` | Maximum of CPU-budget and memory-budget percentages. |
| `status.idleWorkloads` | array of `IdleWorkload` | no | empty | Deployments currently identified as idle. |
| `status.idleWorkloads[].name` | string | yes in item | none | Deployment name. |
| `status.idleWorkloads[].namespace` | string | yes in item | none | Deployment namespace. |
| `status.idleWorkloads[].idleSince` | `metav1.Time` | yes in item | none | Time associated with the idle observation. |
| `status.lastReportRef` | string | no | empty | Reference to the latest generated CostReport. |
| `status.conditions` | array of `metav1.Condition` | no | empty | `UsageWarning` and `BudgetExceeded` condition entries. |

`metav1.Condition` contains the Kubernetes standard fields `type`, `status`, `observedGeneration`, `lastTransitionTime`, `reason`, and `message`. `lastTransitionTime` changes only when the condition state changes.

## CostReport

`CostReportSpec` is currently an empty object. The operator creates reports; users should not need to populate report configuration.

### Status

| Field | Type | Required | Default | Description |
|---|---|---:|---|---|
| `status.period` | string | no | empty | Reporting period in `YYYY-MM` format. |
| `status.namespace` | string | no | empty | Namespace represented by the report. |
| `status.totalCost` | `TotalCost` object | no | empty | Total measured usage and estimated cost for the period. |
| `status.totalCost.coreHours` | string | yes in object | none | Total CPU consumption in core-hours. |
| `status.totalCost.gibHours` | string | yes in object | none | Total memory consumption in GiB-hours. |
| `status.totalCost.estimatedUSD` | string | yes in object | none | Estimated USD using configured CPU and memory unit prices. |
| `status.topConsumers` | array of `TopConsumer` | no | empty | Workloads ranked by CPU usage. |
| `status.topConsumers[].workload` | string | yes in item | none | Workload name. |
| `status.topConsumers[].cpuPercent` | integer | yes in item | `0` | Workload's CPU share as a percentage. |
| `status.topConsumers[].memoryPercent` | integer | yes in item | `0` | Workload's memory share as a percentage. |
| `status.topConsumers[].estimatedUSD` | string | yes in item | none | Workload-level estimated USD. |
| `status.idleEvents` | array of `IdleEvent` | no | empty | Recorded idle scale-down events. |
| `status.idleEvents[].workload` | string | no | empty | Workload involved in the idle event. |
| `status.idleEvents[].scaledDownAt` | string | no | empty | Event timestamp. |
| `status.idleEvents[].estimatedSaved` | string | no | empty | Estimated savings associated with the event. |
| `status.suspensionEvents` | array of `SuspensionEvent` | no | empty | Recorded hard-limit suspension events. |
| `status.suspensionEvents[].workload` | string | yes in item | none | Workload scaled down by suspension. |
| `status.suspensionEvents[].scaledDownAt` | string | yes in item | none | Suspension timestamp. |
| `status.suspensionEvents[].reason` | string | no | empty | Reason for the suspension. |
| `status.conditions` | array of `metav1.Condition` | no | empty | Standard report conditions, when populated. |
