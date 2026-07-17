package metrics

const (
	NamespaceCPUQuery = `sum(rate(container_cpu_usage_seconds_total{namespace=%q}[5m])) by (pod)`
	NamespaceMemQuery = `sum(container_memory_working_set_bytes{namespace=%q}) by (pod)`
)

func ComputeCoreHours(samples []Sample, elapsedHours float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total float64
	for _, s := range samples {
		total += s.Value
	}
	return total / float64(len(samples)) * elapsedHours
}

func ComputeGiBHours(samples []Sample, elapsedHours float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total float64
	for _, s := range samples {
		total += s.Value
	}
	avgBytes := total / float64(len(samples))
	return avgBytes / (1024 * 1024 * 1024) * elapsedHours
}

func AverageCPUPercent(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var total float64
	for _, s := range samples {
		total += s.Value
	}
	return total / float64(len(samples)) * 100
}

// SumValues sums the Value field across all samples.
// Used to collapse per-pod Prometheus results into a single namespace figure.
func SumValues(samples []Sample) float64 {
	total := 0.0
	for _, s := range samples {
		total += s.Value
	}
	return total
}
