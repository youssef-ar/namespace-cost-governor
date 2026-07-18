package idle

import (
	"context"
	"fmt"
	"strings"
	"time"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type IdleWorkload struct {
	Name      string
	Namespace string
	IdleSince time.Time
}

func DetectIdle(
	ctx context.Context,
	metricsClient metrics.Client,
	namespace string,
	threshold float64, // CPU cores below this = idle (e.g. 0.01)
	window time.Duration,
) ([]IdleWorkload, error) {

	end := time.Now()
	start := end.Add(-window)

	// Query average CPU per pod over the window
	query := fmt.Sprintf(
		`avg_over_time(
            rate(container_cpu_usage_seconds_total{namespace="%s", container!=""}[5m])
        [%s:1m])`,
		namespace,
		promDuration(window), // converts time.Duration → "30m", "1h" etc.
	)

	samples, err := metricsClient.QueryRange(ctx, query, start, end, 60)
	if err != nil {
		return nil, fmt.Errorf("querying idle metrics: %w", err)
	}

	// Group samples by pod, compute average value
	podAvg := averageByPod(samples)

	var idle []IdleWorkload
	for pod, avgCPU := range podAvg {
		if avgCPU < threshold {
			idle = append(idle, IdleWorkload{
				Name:      podToDeployment(pod), // strip the random suffix
				Namespace: namespace,
				IdleSince: start, // conservative — idle for at least the window
			})
		}
	}

	return idle, nil
}

// averageByPod collapses []Sample into a map of pod → mean value
func averageByPod(samples []metrics.Sample) map[string]float64 {
	sums := map[string]float64{}
	counts := map[string]int{}
	for _, s := range samples {
		sums[s.Pod] += s.Value
		counts[s.Pod]++
	}
	result := map[string]float64{}
	for pod, sum := range sums {
		result[pod] = sum / float64(counts[pod])
	}
	return result
}

// promDuration converts a time.Duration to a Prometheus duration string
func promDuration(d time.Duration) string {
	if d.Hours() >= 1 {
		return fmt.Sprintf("%.0fh", d.Hours())
	}
	return fmt.Sprintf("%.0fm", d.Minutes())
}

// podToDeployment strips the two trailing random suffixes from a pod name.
// payments-api-6d4f9b-xkq2p → payments-api
// This is a heuristic — works for Deployments, not for StatefulSets.
func podToDeployment(podName string) string {
	parts := strings.Split(podName, "-")
	if len(parts) <= 2 {
		return podName
	}
	return strings.Join(parts[:len(parts)-2], "-")
}

func IsExcluded(workloadName string, exclusions []costv1alpha1.Exclusion) bool {
	for _, ex := range exclusions {
		// Name-based exclusion
		if ex.Name != "" && ex.Name == workloadName {
			return true
		}
		// Label selector exclusion — checked by the caller before passing here
		// (label matching requires the actual Deployment object, not just a name)
	}
	return false
}

func IsLabelExcluded(d appsv1.Deployment, exclusions []costv1alpha1.Exclusion) bool {
	for _, ex := range exclusions {
		if ex.LabelSelector == nil {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(ex.LabelSelector)
		if err != nil {
			// malformed selector in spec — skip it rather than panic
			continue
		}
		if selector.Matches(labels.Set(d.Labels)) {
			return true
		}
	}
	return false
}
