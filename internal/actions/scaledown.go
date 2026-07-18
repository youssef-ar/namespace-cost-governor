package actions

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/youssef-ar/namespace-cost-governor/internal/idle"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AnnotationScaledDownBy     = "cost.platform.io/scaled-down-by"
	AnnotationScaledDownAt     = "cost.platform.io/scaled-down-at"
	AnnotationOriginalReplicas = "cost.platform.io/original-replicas"
	AnnotationReason           = "cost.platform.io/reason"
)

func ScaleDown(ctx context.Context, c client.Client, workload idle.IdleWorkload) error {
	deployment := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{
		Name:      workload.Name,
		Namespace: workload.Namespace,
	}, deployment); err != nil {
		return fmt.Errorf("fetching deployment %s: %w", workload.Name, err)
	}

	// Idempotency check — already scaled down by the operator, skip
	if IsScaledDownByOperator(*deployment) {
		return nil
	}

	// Also skip if already at 0 for another reason — don't overwrite state we don't own
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		return nil
	}

	originalReplicas := int32(1) // safe default
	if deployment.Spec.Replicas != nil {
		originalReplicas = *deployment.Spec.Replicas
	}

	// Patch 1 — write annotations before touching replicas
	patch := client.MergeFrom(deployment.DeepCopy())
	if deployment.Annotations == nil {
		deployment.Annotations = map[string]string{}
	}
	deployment.Annotations[AnnotationScaledDownBy] = "namespace-cost-governor"
	deployment.Annotations[AnnotationScaledDownAt] = time.Now().UTC().Format(time.RFC3339)
	deployment.Annotations[AnnotationOriginalReplicas] = fmt.Sprintf("%d", originalReplicas)
	deployment.Annotations[AnnotationReason] = "idle:cpu<threshold:30m"

	if err := c.Patch(ctx, deployment, patch); err != nil {
		return fmt.Errorf("writing annotations on %s: %w", workload.Name, err)
	}

	// Patch 2 — now set replicas to 0
	patch = client.MergeFrom(deployment.DeepCopy())
	zero := int32(0)
	deployment.Spec.Replicas = &zero

	if err := c.Patch(ctx, deployment, patch); err != nil {
		return fmt.Errorf("scaling down %s: %w", workload.Name, err)
	}

	return nil
}

func Restore(ctx context.Context, c client.Client, namespace, deploymentName string) error {
	deployment := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{
		Name:      deploymentName,
		Namespace: namespace,
	}, deployment); err != nil {
		return fmt.Errorf("fetching deployment %s: %w", deploymentName, err)
	}

	// Only restore if we were the ones who scaled it down
	if !IsScaledDownByOperator(*deployment) {
		return nil
	}

	// Read original replica count from annotation
	originalStr, ok := deployment.Annotations[AnnotationOriginalReplicas]
	if !ok {
		return fmt.Errorf("deployment %s missing original-replicas annotation", deploymentName)
	}
	original, err := strconv.ParseInt(originalStr, 10, 32)
	if err != nil {
		return fmt.Errorf("parsing original replicas for %s: %w", deploymentName, err)
	}

	// Patch 1 — restore replicas first
	patch := client.MergeFrom(deployment.DeepCopy())
	replicas := int32(original)
	deployment.Spec.Replicas = &replicas

	if err := c.Patch(ctx, deployment, patch); err != nil {
		return fmt.Errorf("restoring replicas on %s: %w", deploymentName, err)
	}

	// Patch 2 — remove the cost annotations
	patch = client.MergeFrom(deployment.DeepCopy())
	delete(deployment.Annotations, AnnotationScaledDownBy)
	delete(deployment.Annotations, AnnotationScaledDownAt)
	delete(deployment.Annotations, AnnotationOriginalReplicas)
	delete(deployment.Annotations, AnnotationReason)

	if err := c.Patch(ctx, deployment, patch); err != nil {
		return fmt.Errorf("removing annotations on %s: %w", deploymentName, err)
	}

	return nil
}

func IsScaledDownByOperator(d appsv1.Deployment) bool {
	if d.Annotations == nil {
		return false
	}
	return d.Annotations[AnnotationScaledDownBy] == "namespace-cost-governor"
}
