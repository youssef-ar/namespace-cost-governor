# Cost model

## CPU usage

Prometheus returns per-pod CPU usage as a rate derived from `container_cpu_usage_seconds_total`. A rate is measured in CPU cores: `1.0` means one fully used core. For an interval of duration $\Delta t$ hours, the incremental CPU consumption is:

$$
\Delta\text{core-hours} = \left(\sum_{pods} \text{CPU cores}\right) \times \Delta t
$$

The operator adds this value to `status.accumulatedUsage.coreHours` on each reconciliation. For example, three pods using `0.25`, `0.50`, and `0.25` cores for 30 minutes consume:

$$
(0.25 + 0.50 + 0.25) \times 0.5 = 0.5\text{ core-hours}
$$

## Memory usage

Prometheus returns per-pod `container_memory_working_set_bytes`. The operator converts the sum to GiB and integrates it over the interval:

$$
\Delta\text{GiB-hours} = \left(\frac{\sum_{pods}\text{working-set bytes}}{2^{30}}\right) \times \Delta t
$$

For example, 4 GiB of working-set memory held for 30 minutes contributes $4 \times 0.5 = 2$ GiB-hours.

## Budget percentage and phase

CPU and memory are compared independently with the monthly values in `spec.monthly`:

$$
\text{cpuPercent} = 100 \times \frac{\text{accumulated core-hours}}{\text{monthly CPU core-hours}}
$$

$$
\text{memoryPercent} = 100 \times \frac{\text{accumulated GiB-hours}}{\text{monthly memory GiB-hours}}
$$

`status.budgetPercent` is the greater of those two percentages, rounded to the integer exposed by status. This prevents one dimension from masking exhaustion of the other. The resulting value drives the phase thresholds: 80% (`Warning`), 100% (`Exceeded`), and 120% (`Suspended`).

## Unit prices

The operator estimates cost as:

$$
\text{estimatedUSD} = (\text{core-hours} \times \text{CPU price}) + (\text{GiB-hours} \times \text{memory price})
$$

The prices are supplied through `CPU_PRICE_PER_CORE_HOUR` and `MEM_PRICE_PER_GIB_HOUR`. The Helm chart renders these values from `pricing.cpuPricePerCoreHour` and `pricing.memPricePerGiBHour` into the pricing ConfigMap; the Deployment reads the ConfigMap as environment variables. The chart defaults are `0.048` USD per core-hour and `0.006` USD per GiB-hour. Change them to match the accounting policy for the cluster.

## Meaning of estimated USD

The USD value is an estimate based on the configured unit prices and measured resource time. It is useful for comparing namespaces and applying a simple budget policy. It is not a cloud-provider bill and should not be reconciled directly to an invoice. It excludes, unless represented by the configured prices and metrics, network egress, persistent storage, requests for unused capacity, control-plane charges, licenses, reserved-capacity effects, spot discounts, taxes, and other provider-specific adjustments.

## Idle detection

A workload is considered idle when its average CPU usage remains below `spec.idleThreshold.cpuPercent` percent of its requested CPU over `spec.idleThreshold.window`. The operator queries Prometheus with `avg_over_time`, collapses pod samples into a per-Deployment average, and verifies the Deployment through the Kubernetes API. Non-Deployment workloads are not treated as idle candidates.

CPU is used because it is a direct activity signal and has a meaningful continuously sampled rate. Memory is deliberately not used for idleness: a process can retain memory while doing no useful work, and a memory threshold would classify caches and stable heaps poorly. The time window prevents a short pause or scrape anomaly from causing a scale-down.

Idle detection is separate from accounting. An idle Deployment is only acted on when the configured phase action includes `scaleDownIdle: true`; exclusions always take precedence.

## Limitations

- The estimate inherits Prometheus scrape resolution, query semantics, label quality, and availability.
- Reconciliation is periodic, so short-lived usage between observations can be undercounted.
- Incremental accumulation does not reconstruct downtime or historical gaps.
- CPU and memory are container metrics, not a complete allocation model.
- Working-set memory is not equivalent to billed memory on every platform.
- The configured rates are static and do not model tiered, regional, committed-use, reserved, or spot pricing.
- A scale-down changes future usage but does not retroactively change the recorded usage.
