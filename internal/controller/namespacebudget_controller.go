/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	actions "github.com/youssef-ar/namespace-cost-governor/internal/actions"
	"github.com/youssef-ar/namespace-cost-governor/internal/report"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/idle"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespaceBudgetReconciler reconciles a NamespaceBudget object
type NamespaceBudgetReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	PrometheusClient    *metrics.Client
	SlackWebhookURL     string
	CPUPricePerCoreHour float64
	MemPricePerGiBHour  float64
}

const (
	phaseOK        = "OK"
	phaseWarning   = "Warning"
	phaseExceeded  = "Exceeded"
	phaseSuspended = "Suspended"

	warningThreshold   = 0.80
	exceededThreshold  = 1.00
	suspendedThreshold = 1.20

	conditionUsageWarning   = "UsageWarning"
	conditionBudgetExceeded = "BudgetExceeded"

	namespaceBudgetFinalizer = "cost.platform.io/cleanup"
)

// +kubebuilder:rbac:groups=cost.cost.platform.io,resources=namespacebudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cost.cost.platform.io,resources=namespacebudgets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cost.cost.platform.io,resources=namespacebudgets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NamespaceBudget object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *NamespaceBudgetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	// step1: Fetch the NamespaceBudget instance
	budget := &costv1alpha1.NamespaceBudget{}
	if err := r.Get(ctx, req.NamespacedName, budget); err != nil {
		if apierrors.IsNotFound(err) {
			// Object was deleted — nothing to do, not an error.
			return ctrl.Result{}, nil
		}
		// Any other error (network issue, RBAC, etc.) is worth logging.
		logger.Error(err, "unable to fetch NamespaceBudget")
		return ctrl.Result{}, err
	}
	now := time.Now()
	// Ensure Accumulated.Since is initialized on first reconcile
	if budget.Status.Accumulated.Since == "" {
		budget.Status.Accumulated.Since = now.Format(time.RFC3339)
	}
	// Trigger on the 1st of the month, after the first reconcile of the day
	if now.Day() == 1 && budget.Status.Accumulated.Since != "" {
		since, _ := time.Parse(time.RFC3339, budget.Status.Accumulated.Since)
		// Only generate if the report covers the previous month
		if since.Month() != now.Month() {
			if err := r.maybeGenerateReport(ctx, budget); err != nil {
				logger.Error(err, "failed to generate monthly cost report")
				// non-fatal
			}
			// Reset accumulation for the new month
			budget.Status.Accumulated.CoreHours = "0"
			budget.Status.Accumulated.GiBHours = "0"
			budget.Status.Accumulated.Since = now.Format(time.RFC3339)
		}
	}
	previousPhase := budget.Status.Phase
	// step2: handle deletion
	if !budget.DeletionTimestamp.IsZero() {

		logger.Info("NamespaceBudget deletion detected")

		// Run cleanup logic
		if controllerutil.ContainsFinalizer(budget, namespaceBudgetFinalizer) {
			if err := r.cleanup(ctx, budget); err != nil {
				return ctrl.Result{}, err
			}

			// Remove finalizer
			controllerutil.RemoveFinalizer(
				budget,
				namespaceBudgetFinalizer,
			)

			if err := r.Update(ctx, budget); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}
	// step3: register finalizer if not present
	if !controllerutil.ContainsFinalizer(
		budget,
		namespaceBudgetFinalizer,
	) {

		logger.Info("Adding finalizer")

		controllerutil.AddFinalizer(
			budget,
			namespaceBudgetFinalizer,
		)

		if err := r.Update(ctx, budget); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// step 4: Query Prometheus for CPU + memory

	cpuSamples, err := r.PrometheusClient.QueryInstant(ctx,
		fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="%s", container!=""}[5m]))`,
			req.Namespace))
	if err != nil {
		// log the error, requeue, and return
		logger.Error(err, "unable to query CPU usage from Prometheus")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	memSamples, err := r.PrometheusClient.QueryInstant(ctx,
		fmt.Sprintf(`sum by (pod) (container_memory_working_set_bytes{namespace="%s", container!=""})`,
			req.Namespace))
	if err != nil {
		logger.Error(err, "unable to query memory usage from Prometheus")
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// step 5: Compute elapsed time since last reconcile
	// step 5: Accumulate core-hours and GiB-hours into status
	now = time.Now()
	lastReconcile := budget.Status.LastReconcile.Time
	if lastReconcile.IsZero() {
		lastReconcile = now // first reconcile, no elapsed time to accumulate
	}
	elapsedHours := now.Sub(lastReconcile).Hours()

	// - Sum all pod values into a single namespace-level figure
	currentCPUCores := metrics.SumValues(cpuSamples)
	currentMemBytes := metrics.SumValues(memSamples)
	currentMemGiB := currentMemBytes / (1024 * 1024 * 1024)

	// - Add this tick's consumption to the running total
	prevCore, _ := strconv.ParseFloat(budget.Status.Accumulated.CoreHours, 64)
	prevCore += currentCPUCores * elapsedHours
	budget.Status.Accumulated.CoreHours = fmt.Sprintf("%.6f", prevCore)

	prevGiB, _ := strconv.ParseFloat(budget.Status.Accumulated.GiBHours, 64)
	prevGiB += currentMemGiB * elapsedHours
	budget.Status.Accumulated.GiBHours = fmt.Sprintf("%.6f", prevGiB)
	budget.Status.LastReconcile = metav1.NewTime(now)

	// - Update the snapshot of current (instantaneous) usage for display
	budget.Status.CurrentUsage.Cpu = fmt.Sprintf("%.6f", currentCPUCores)
	budget.Status.CurrentUsage.Memory = fmt.Sprintf("%.6f", currentMemGiB)

	// step 6: Compute budgetPercent + phase
	monthlyCPU, _ := strconv.ParseFloat(budget.Spec.Monthly.Cpu, 64)
	monthlyMem, _ := strconv.ParseFloat(budget.Spec.Monthly.Memory, 64)

	cpuPercent := 0.0
	memPercent := 0.0
	if monthlyCPU > 0 {
		accCore, _ := strconv.ParseFloat(budget.Status.Accumulated.CoreHours, 64)
		cpuPercent = (accCore / monthlyCPU) * 100
	}
	if monthlyMem > 0 {
		accGiB, _ := strconv.ParseFloat(budget.Status.Accumulated.GiBHours, 64)
		memPercent = (accGiB / monthlyMem) * 100
	}

	budgetPercent := math.Max(cpuPercent, memPercent)

	// Determine phase
	phase := determinePhase(budgetPercent)
	phaseChanged := previousPhase != phase

	// step 7: update status conditions
	if err := r.updateStatus(ctx, budget, phase, int(budgetPercent)); err != nil {
		logger.Error(err, "unable to update NamespaceBudget status")
		return ctrl.Result{}, err
	}

	// step 8: Detect idle workloads if budget is exceeded or suspended
	// Only run idle detection if budget is under pressure

	var actionable []idle.IdleWorkload
	if phase == "Exceeded" || phase == "Suspended" {
		window, err := parseDuration(
			budget.Spec.IdleThreshold.Window,
		)

		if err != nil {
			logger.Error(err, "invalid idle threshold window")
			return ctrl.Result{}, err
		}
		idleWorkloads, err := idle.DetectIdle(
			ctx,
			*r.PrometheusClient,
			r.Client,
			req.Namespace,
			float64(budget.Spec.IdleThreshold.CpuPercent),
			window,
		)
		if err != nil {
			logger.Error(err, "failed to detect idle workloads")
			// non-fatal — continue without idle detection this tick
		} else {
			// Filter out excluded workloads before storing or acting

			for _, w := range idleWorkloads {
				// Check name-based exclusion first (no API call needed)
				if idle.IsExcluded(w.Name, budget.Spec.Exclusions) {
					continue
				}

				// Fetch the Deployment to check label-based exclusion
				deployment := &appsv1.Deployment{}
				if err := r.Get(ctx, types.NamespacedName{
					Name:      w.Name,
					Namespace: w.Namespace,
				}, deployment); err != nil {
					// Deployment may have been deleted between detection and now
					logger.Error(err, "failed to fetch deployment for exclusion check", "deployment", w.Name)
					continue
				}

				if idle.IsLabelExcluded(*deployment, budget.Spec.Exclusions) {
					continue
				}

				actionable = append(actionable, w)
			}

			// Persist the filtered idle workload list in status for visibility
			// Preserve any previously observed IdleSince timestamps to avoid
			// causing a status diff every reconcile (which would requeue immediately).
			budget.Status.IdleWorkloads = toIdleWorkloadStatus(actionable, budget.Status.IdleWorkloads)
			if err := r.updateStatus(ctx, budget, phase, budget.Status.BudgetPercent); err != nil {
				logger.Error(err, "failed to update status with idle workloads")
			}
		}
	}

	// step9: Scale down actionable idle workloads (Exceeded and Suspended phase)
	affectedNames := make([]string, 0, len(actionable))
	if phase == "Exceeded" || phase == "Suspended" {
		for _, w := range actionable {
			if err := actions.ScaleDown(ctx, r.Client, w); err != nil {
				logger.Error(err, "failed to scale down idle workload", "deployment", w.Name)
				// non-fatal — log and continue to next workload
				continue
			}
			affectedNames = append(affectedNames, w.Name)
		}
	}

	// step10: Suspend all workloads if in Suspended phase
	if phase == "Suspended" {
		suspendedNames, err := actions.SuspendAll(ctx, r.Client, req.Namespace, budget.Spec.Exclusions)
		if err != nil {
			logger.Error(err, "failed to suspend all workloads")
		}
		affectedNames = append(affectedNames, suspendedNames...)
	}
	// step11: Notify on phase transition only
	if phaseChanged && r.SlackWebhookURL != "" {
		msg := actions.BuildMessage(req.Namespace, phase, budget.Status.BudgetPercent, affectedNames)
		if err := actions.SendSlack(ctx, r.SlackWebhookURL, msg); err != nil {
			logger.Error(err, "failed to send slack notification")
		}
	}

	return ctrl.Result{
		RequeueAfter: time.Minute,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceBudgetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&costv1alpha1.NamespaceBudget{}).
		Named("namespacebudget").
		Complete(r)
}

func (r *NamespaceBudgetReconciler) cleanup(
	ctx context.Context,
	budget *costv1alpha1.NamespaceBudget,
) error {

	logger := logf.FromContext(ctx)

	logger.Info(
		"Running cleanup for NamespaceBudget",
		"name", budget.Name,
	)

	// TODO:
	// - remove cost policies
	// - delete generated resources
	// - release quotas
	// - clean metrics

	return nil
}

func determinePhase(percent float64) string {
	switch {
	case percent >= 120:
		return "Suspended"
	case percent >= 100:
		return "Exceeded"
	case percent >= 80:
		return "Warning"
	default:
		return "OK"
	}
}

func (r *NamespaceBudgetReconciler) updateStatus(
	ctx context.Context,
	budget *costv1alpha1.NamespaceBudget,
	phase string,
	budgetPercent int,
) error {
	now := metav1.Now()

	budget.Status.Phase = phase
	budget.Status.BudgetPercent = budgetPercent

	// UsageWarning condition
	setCondition(&budget.Status.Conditions, metav1.Condition{
		Type:               "UsageWarning",
		Status:             conditionStatus(budgetPercent >= 80),
		Reason:             "BudgetThresholdReached",
		Message:            fmt.Sprintf("Budget usage at %d%%", budgetPercent),
		LastTransitionTime: now,
	})

	// BudgetExceeded condition
	setCondition(&budget.Status.Conditions, metav1.Condition{
		Type:               "BudgetExceeded",
		Status:             conditionStatus(budgetPercent >= 100),
		Reason:             "MonthlyBudgetExceeded",
		Message:            fmt.Sprintf("Budget usage at %d%%", budgetPercent),
		LastTransitionTime: now,
	})

	return r.Status().Update(ctx, budget)
}

func conditionStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// setCondition updates an existing condition or appends a new one.
// Critically: it only updates LastTransitionTime when Status actually changes.
func setCondition(conditions *[]metav1.Condition, new metav1.Condition) {
	for i, existing := range *conditions {
		if existing.Type == new.Type {
			if existing.Status == new.Status {
				// Status unchanged — preserve the original LastTransitionTime
				new.LastTransitionTime = existing.LastTransitionTime
			}
			(*conditions)[i] = new
			return
		}
	}
	*conditions = append(*conditions, new)
}

func toIdleWorkloadStatus(workloads []idle.IdleWorkload, previous []costv1alpha1.IdleWorkload) []costv1alpha1.IdleWorkload {
	// Build a lookup of previous IdleSince by namespace/name so we can
	// preserve the original timestamp when a workload remains idle across
	// reconciles. This prevents status-only changes (moving timestamps) that
	// would otherwise re-trigger reconciliation continuously.
	prevMap := make(map[string]metav1.Time, len(previous))
	for _, p := range previous {
		key := p.Namespace + "/" + p.Name
		prevMap[key] = p.IdleSince
	}

	result := make([]costv1alpha1.IdleWorkload, 0, len(workloads))
	for _, w := range workloads {
		key := w.Namespace + "/" + w.Name
		idleSince := metav1.NewTime(w.IdleSince)
		if t, ok := prevMap[key]; ok && !t.IsZero() {
			// reuse previously-observed IdleSince
			idleSince = t
		}
		result = append(result, costv1alpha1.IdleWorkload{
			Name:      w.Name,
			Namespace: w.Namespace,
			IdleSince: idleSince,
		})
	}
	return result
}

func parseDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, err)
	}

	return duration, nil
}

func (r *NamespaceBudgetReconciler) maybeGenerateReport(
	ctx context.Context,
	budget *costv1alpha1.NamespaceBudget,
) error {
	g := report.NewGenerator(
		*r.PrometheusClient,
		r.Client,
		r.CPUPricePerCoreHour,
		r.MemPricePerGiBHour,
	)
	return g.Generate(ctx, *budget)
}
