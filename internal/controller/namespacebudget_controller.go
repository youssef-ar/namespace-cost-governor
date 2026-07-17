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

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespaceBudgetReconciler reconciles a NamespaceBudget object
type NamespaceBudgetReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	PrometheusClient *metrics.Client
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
	now := time.Now()
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
	budget.Status.Accumulated.CoreHours += currentCPUCores * elapsedHours
	budget.Status.Accumulated.GiBHours += currentMemGiB * elapsedHours
	budget.Status.LastReconcile = metav1.NewTime(now)

	// - Update the snapshot of current (instantaneous) usage for display
	budget.Status.CurrentUsage.Cpu = fmt.Sprintf("%.3f", currentCPUCores)
	budget.Status.CurrentUsage.Memory = fmt.Sprintf("%.3f", currentMemGiB)

	// step 6: Compute budgetPercent + phase
	monthlyCPU, _ := strconv.ParseFloat(budget.Spec.Monthly.Cpu, 64)
	monthlyMem, _ := strconv.ParseFloat(budget.Spec.Monthly.Memory, 64)

	cpuPercent := 0.0
	memPercent := 0.0
	if monthlyCPU > 0 {
		cpuPercent = (budget.Status.Accumulated.CoreHours / monthlyCPU) * 100
	}
	if monthlyMem > 0 {
		memPercent = (budget.Status.Accumulated.GiBHours / monthlyMem) * 100
	}

	budgetPercent := math.Max(cpuPercent, memPercent)

	// Determine phase
	phase := determinePhase(budgetPercent)

	// step 7: update status conditions
	if err := r.updateStatus(ctx, budget, phase, int(budgetPercent)); err != nil {
		logger.Error(err, "unable to update NamespaceBudget status")
		return ctrl.Result{}, err
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
