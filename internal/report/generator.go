package report

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/actions"
	"github.com/youssef-ar/namespace-cost-governor/internal/idle"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Generator struct {
	MetricsClient       metrics.Client `json:"metricsClient"`
	Client              client.Client  `json:"client"`
	CPUPricePerCoreHour float64        `json:"cpuPricePerCoreHour"` // e.g. 0.048
	MemPricePerGiBHour  float64        `json:"memPricePerGiBHour"`  // e.g. 0.006
}

type WorkloadUsage struct {
	Name       string  `json:"name"`
	CoreHours  float64 `json:"coreHours"`
	GiBHours   float64 `json:"giBHours"`
	CPUPercent float64 `json:"cpuPercent"`
	MemPercent float64 `json:"memPercent"`
}

func NewGenerator(metricsClient metrics.Client, client client.Client, cpuPricePerCoreHour, memPricePerGiBHour float64) *Generator {
	return &Generator{
		MetricsClient:       metricsClient,
		Client:              client,
		CPUPricePerCoreHour: cpuPricePerCoreHour,
		MemPricePerGiBHour:  memPricePerGiBHour,
	}
}

func (g *Generator) Generate(ctx context.Context, budget costv1alpha1.NamespaceBudget) error {
	now := time.Now()
	// Report covers the previous month
	firstOfLastMonth := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	lastOfLastMonth := firstOfLastMonth.AddDate(0, 1, 0).Add(-time.Second)
	period := firstOfLastMonth.Format("2006-01")

	// Check if report already exists — don't generate twice
	existing := &costv1alpha1.CostReport{}
	reportRef := reportName(budget.Namespace, firstOfLastMonth)
	err := g.Client.Get(ctx, types.NamespacedName{
		Name:      reportRef,
		Namespace: budget.Namespace,
	}, existing)
	if err == nil {
		// already exists
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking existing report: %w", err)
	}

	// Query per-workload usage over the whole month
	workloads, err := g.queryWorkloadUsage(ctx, budget.Namespace, firstOfLastMonth, lastOfLastMonth)
	if err != nil {
		return fmt.Errorf("querying workload usage: %w", err)
	}

	// Total comes from accumulated status — already computed incrementally
	totalCoreHours, err := strconv.ParseFloat(budget.Status.Accumulated.CoreHours, 64)
	if err != nil {
		totalCoreHours = 0
	}
	totalGiBHours, err := strconv.ParseFloat(budget.Status.Accumulated.GiBHours, 64)
	if err != nil {
		totalGiBHours = 0
	}
	totalUSD := g.estimateUSD(totalCoreHours, totalGiBHours)

	// Build top consumers sorted by CPU descending
	topConsumers := g.buildTopConsumers(workloads, totalCoreHours, totalGiBHours)

	// Collect suspension events from deployment annotations
	suspensionEvents, err := g.collectSuspensionEvents(ctx, budget.Namespace)
	if err != nil {
		// non-fatal — report without events rather than failing
		suspensionEvents = []costv1alpha1.SuspensionEvent{}
	}

	// Create the CostReport first. Status is a subresource and must be written
	// separately after the object exists.
	report := &costv1alpha1.CostReport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      reportRef,
			Namespace: budget.Namespace,
		},
	}

	if err := g.Client.Create(ctx, report); err != nil {
		return fmt.Errorf("creating cost report: %w", err)
	}

	report.Status = costv1alpha1.CostReportStatus{
		Period:    period,
		NameSpace: budget.Namespace,
		TotalCost: costv1alpha1.TotalCost{
			CoreHours:    fmt.Sprintf("%.2f", totalCoreHours),
			GiBHours:     fmt.Sprintf("%.2f", totalGiBHours),
			EstimatedUSD: fmt.Sprintf("$%.2f", totalUSD),
		},
		TopConsumers:    topConsumers,
		SuspendedEvents: suspensionEvents,
	}

	if err := g.Client.Status().Update(ctx, report); err != nil {
		return fmt.Errorf("updating cost report status: %w", err)
	}

	return nil
}

func (g *Generator) queryWorkloadUsage(
	ctx context.Context,
	namespace string,
	start, end time.Time,
) ([]WorkloadUsage, error) {

	// CPU: average rate per pod over the month, sampled hourly
	cpuQuery := fmt.Sprintf(
		`avg_over_time(rate(container_cpu_usage_seconds_total{namespace="%s",container!=""}[5m])[1h:5m])`,
		namespace,
	)
	cpuSamples, err := g.MetricsClient.QueryRange(ctx, cpuQuery, start, end, int(time.Hour.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("querying cpu usage: %w", err)
	}

	// Memory: average working set per pod over the month, sampled hourly
	memQuery := fmt.Sprintf(
		`avg_over_time(container_memory_working_set_bytes{namespace="%s",container!=""}[1h])`,
		namespace,
	)
	memSamples, err := g.MetricsClient.QueryRange(ctx, memQuery, start, end, int(time.Hour.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("querying memory usage: %w", err)
	}

	// Aggregate per pod → per deployment (strip pod suffix)
	cpuByDeployment := aggregateByDeployment(cpuSamples)
	memByDeployment := aggregateByDeployment(memSamples)

	// Compute core-hours and GiB-hours per deployment
	// Each sample is an hourly average, so multiply by 1h to get core-hours
	elapsedHours := end.Sub(start).Hours()

	var workloads []WorkloadUsage
	for name, avgCPU := range cpuByDeployment {
		avgMem := memByDeployment[name] / (1024 * 1024 * 1024) // bytes → GiB
		workloads = append(workloads, WorkloadUsage{
			Name:      name,
			CoreHours: avgCPU * elapsedHours,
			GiBHours:  avgMem * elapsedHours,
		})
	}

	return workloads, nil
}

// aggregateByDeployment collapses per-pod samples into per-deployment averages
func aggregateByDeployment(samples []metrics.Sample) map[string]float64 {
	sums := map[string]float64{}
	counts := map[string]int{}
	for _, s := range samples {
		deployment := idle.PodToDeployment(s.Pod)
		sums[deployment] += s.Value
		counts[deployment]++
	}
	result := map[string]float64{}
	for name, sum := range sums {
		result[name] = sum / float64(counts[name])
	}
	return result
}

func (g *Generator) buildTopConsumers(
	workloads []WorkloadUsage,
	totalCoreHours float64,
	totalGiBHours float64,
) []costv1alpha1.TopConsumer {

	// Sort by CPU core-hours descending
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].CoreHours > workloads[j].CoreHours
	})

	// Take top 5
	limit := 5
	if len(workloads) < limit {
		limit = len(workloads)
	}

	consumers := make([]costv1alpha1.TopConsumer, 0, limit)
	for _, w := range workloads[:limit] {
		cpuPct := 0.0
		memPct := 0.0
		if totalCoreHours > 0 {
			cpuPct = (w.CoreHours / totalCoreHours) * 100
		}
		if totalGiBHours > 0 {
			memPct = (w.GiBHours / totalGiBHours) * 100
		}
		consumers = append(consumers, costv1alpha1.TopConsumer{
			Workload:      w.Name,
			CPUPercent:    int(cpuPct),
			MemoryPercent: int(memPct),
			EstimatedUSD:  fmt.Sprintf("$%.2f", g.estimateUSD(w.CoreHours, w.GiBHours)),
		})
	}

	return consumers
}

func (g *Generator) collectSuspensionEvents(
	ctx context.Context,
	namespace string,
) ([]costv1alpha1.SuspensionEvent, error) {

	deploymentList := &appsv1.DeploymentList{}
	if err := g.Client.List(ctx, deploymentList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}

	var events []costv1alpha1.SuspensionEvent
	for _, d := range deploymentList.Items {
		scaledDownAt, ok := d.Annotations[actions.AnnotationScaledDownAt]
		if !ok {
			continue
		}
		events = append(events, costv1alpha1.SuspensionEvent{
			Workload:     d.Name,
			ScaledDownAt: scaledDownAt,
			Reason:       d.Annotations[actions.AnnotationReason],
		})
	}

	return events, nil
}

func (g *Generator) estimateUSD(coreHours, gibHours float64) float64 {
	return (coreHours * g.CPUPricePerCoreHour) + (gibHours * g.MemPricePerGiBHour)
}

func reportName(namespace string, t time.Time) string {
	// e.g. "team-payments-2024-01"
	return fmt.Sprintf("%s-%s", namespace, t.Format("2006-01"))
}
