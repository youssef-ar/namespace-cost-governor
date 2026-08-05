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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type IdleWorkload struct {
	Name      string
	Namespace string
	IdleSince time.Time
}

func DetectIdle(
	ctx context.Context,
	metricsClient metrics.Client,
	kubeClient client.Client,
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

	// Aggregate per-deployment (average across pods) then decide once per deployment
	depSums := map[string]float64{}
	depCounts := map[string]int{}
	for pod, avgCPU := range podAvg {
		dep := PodToDeployment(pod)
		depSums[dep] += avgCPU
		depCounts[dep]++
	}

	var idleList []IdleWorkload
	for dep, sum := range depSums {
		avg := sum / float64(depCounts[dep])
		if avg < threshold {
			idleList = append(idleList, IdleWorkload{
				Name:      dep,
				Namespace: namespace,
				IdleSince: start,
			})
		}
	}

	// Verify each detected workload actually has a Deployment object
	var verified []IdleWorkload
	for _, w := range idleList {
		dep := &appsv1.Deployment{}
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: w.Namespace, Name: w.Name}, dep); err != nil {
			// not a Deployment or couldn't fetch — skip silently
			continue
		}
		verified = append(verified, w)
	}

	return verified, nil
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
func PodToDeployment(podName string) string {
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
