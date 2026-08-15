# namespace-cost-governor
// TODO(user): Add simple overview of use/purpose

## Description
// TODO(user): An in-depth paragraph about your project and overview of use

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/namespace-cost-governor:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/namespace-cost-governor:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
![CI](https://github.com/youssef-ar/namespace-cost-governor/actions/workflows/ci.yaml/badge.svg) ![Release](https://github.com/youssef-ar/namespace-cost-governor/actions/workflows/release.yaml/badge.svg)

# namespace-cost-governor

`namespace-cost-governor` is a Kubernetes operator for enforcing monthly CPU and memory budgets per namespace in multi-tenant clusters. Platform administrators declare a `NamespaceBudget`; the operator reads per-pod usage from Prometheus, accumulates core-hours and GiB-hours, applies configured warning, idle scale-down, and suspension actions, and creates a monthly `CostReport`.

## Architecture

```text
NamespaceBudget CRD
                |
                v
Operator reconciliation (60 s) <---- Prometheus HTTP API
                |
                +--> status: usage, phase, conditions
                +--> graduated actions: notify / scale idle / suspend all
                |
                v
CostReport CRD (previous month, created on the first day)
```

## Quick start

### Prerequisites

- Kubernetes cluster with `kubectl` access and permission to install cluster-scoped RBAC and CRDs.
- Prometheus exposing `container_cpu_usage_seconds_total` and `container_memory_working_set_bytes`.
- Helm 3 for the chart installation.
- A container registry accessible by the cluster if building the image yourself.

### Install

CRDs are managed separately from the Helm release:

```sh
make install
helm install namespace-cost-governor ./helm/namespace-cost-governor \
    --namespace namespace-cost-governor-system \
    --create-namespace \
    --set image.repository=ghcr.io/youssef-ar/namespace-cost-governor \
    --set image.tag=0.1.0
```

Set the Prometheus endpoint and prices for the target cluster when they differ from the chart defaults:

```sh
helm upgrade --install namespace-cost-governor ./helm/namespace-cost-governor \
    --namespace namespace-cost-governor-system --create-namespace \
    --set prometheus.address=http://prometheus.monitoring:9090 \
    --set pricing.cpuPricePerCoreHour=0.048 \
    --set pricing.memPricePerGiBHour=0.006
```

Apply a budget in the namespace being governed:

```yaml
apiVersion: cost.cost.platform.io/v1alpha1
kind: NamespaceBudget
metadata:
    name: budget
    namespace: payments
spec:
    monthly:
        cpu: "50"
        memory: "200"
    idleThreshold:
        cpuPercent: 5
        window: 30m
    actions:
        onWarning:
            - notify: slack
        onExceeded:
            - notify: slack
            - scaleDownIdle: true
        onHardLimit:
            - notify: slack
            - suspendAll: true
    exclusions:
        - name: payments-db
        - labelSelector:
                matchLabels:
                    cost.platform.io/critical: "true"
```

```sh
kubectl apply -f namespacebudget.yaml
kubectl -n payments get namespacebudget payments -o yaml
```

The Helm chart can receive a Slack webhook with `--set slack.webhookURL=...`; for production use, prefer `slack.existingSecret` and `slack.existingSecretKey`. The chart's `leaderElection.enabled` defaults to `true`.

## Budget phases

The phase is based on the larger of CPU and memory budget usage. Actions are evaluated on transitions into a phase and are not repeated on every reconciliation tick.

| Phase | Threshold | Enforcement |
|---|---:|---|
| `OK` | `< 80%` | No graduated action. |
| `Warning` | `>= 80%` | Configured `onWarning` actions, commonly Slack notification. |
| `Exceeded` | `>= 100%` | Configured `onExceeded` actions; commonly scale idle Deployments to zero. |
| `Suspended` | `>= 120%` | Configured `onHardLimit` actions; `suspendAll` scales all non-excluded Deployments to zero. |

Two status conditions are maintained: `UsageWarning` becomes true at 80%, and `BudgetExceeded` becomes true at 100%. Their `LastTransitionTime` changes only when the condition state changes.

## Exclusions

An exclusion can specify an exact workload name or a Kubernetes `LabelSelector`. Name matching is exact and does not support wildcards. A label selector uses Kubernetes set-based selector semantics. The operator checks exclusions before idle scale-down and suspension; an excluded Deployment is never touched, regardless of phase. Idle discovery still verifies candidate workloads against the Kubernetes API and ignores non-Deployment workloads.

## Cost model

CPU is integrated as core-hours from Prometheus CPU rate; memory is integrated as GiB-hours from working-set bytes. The estimated cost is:

$$\text{core-hours} \times \text{CPU price} + \text{GiB-hours} \times \text{memory price}$$

Prices come from `CPU_PRICE_PER_CORE_HOUR` and `MEM_PRICE_PER_GIB_HOUR`, injected by the Helm pricing ConfigMap. Defaults are `0.048` USD per core-hour and `0.006` USD per GiB-hour. The estimate is not a cloud bill: it excludes egress, storage, taxes, control-plane charges, reservations, and spot discounts. See [docs/cost-model.md](docs/cost-model.md).

## kubectl-budget plugin

The repository builds a standalone `kubectl-budget` binary. Put it on `PATH`; kubectl discovers it as `kubectl budget`.

```sh
go build -o kubectl-budget ./cmd/kubectl-budget
install -m 0755 kubectl-budget ~/.local/bin/kubectl-budget
kubectl budget status payments
```

Example status output:

```text
NAMESPACE  PHASE     BUDGET%  CPU                  MEMORY             LAST RECONCILE
payments   Exceeded  106%     0.083000 cores       1.750000 GiB       2026-08-15 12:00:00

Idle workloads:
    NAME          IDLE SINCE
    payments-api  2026-08-15 11:30:00

Conditions:
    TYPE             STATUS  MESSAGE
    UsageWarning     True    Budget usage at 106%
    BudgetExceeded   True    Budget usage at 106%
```

Fetch the latest monthly report:

```sh
kubectl budget report payments
```

```text
PERIOD   CORE-HOURS  GIB-HOURS  ESTIMATED USD
2026-07  49.250000   188.000000 2.48

TOP CONSUMERS
WORKLOAD       CPU%  MEMORY%  ESTIMATED USD
payments-api   72    61       1.54
payments-jobs  20    29       0.62

SUSPENSION EVENTS
WORKLOAD       SCALED DOWN AT              REASON
payments-api   2026-07-28T12:00:00Z       hard limit
```

Restore Deployments previously scaled down by the operator. The confirmation flag is required:

```sh
kubectl budget restore payments --yes
```

```text
Restored 1 deployment in namespace payments
```

Restore uses the operator's annotations to recover original replicas and removes those annotations. Do not remove them manually before restoration.

## Documentation

- [Design decisions](docs/design.md)
- [Cost model](docs/cost-model.md)
- [API reference](docs/api-reference.md)

## Development and testing

```sh
make lint-fix
make test
```

CI runs lint and generated-code checks, unit tests, and then Kind-based end-to-end tests. Release automation builds and publishes the distroless image and Helm chart for `v*` tags.

## Uninstall

```sh
kubectl delete -f namespacebudget.yaml
helm uninstall namespace-cost-governor -n namespace-cost-governor-system
make uninstall
```

Deleting a `NamespaceBudget` invokes the `cost.platform.io/cleanup` finalizer before the resource is removed.

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
