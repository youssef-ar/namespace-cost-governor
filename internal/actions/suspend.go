package actions

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/idle"
)

func SuspendAll(
	ctx context.Context,
	c client.Client,
	namespace string,
	exclusions []costv1alpha1.Exclusion,
) ([]string, error) {
	var suspended []string
	// List all Deployments in the namespace
	deploymentList := &appsv1.DeploymentList{}
	if err := c.List(ctx, deploymentList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing deployments in %s: %w", namespace, err)
	}

	for _, d := range deploymentList.Items {
		// Skip name-based exclusions
		if idle.IsExcluded(d.Name, exclusions) {
			continue
		}

		// Skip label-based exclusions
		if idle.IsLabelExcluded(d, exclusions) {
			continue
		}

		// Skip if already scaled down by the operator — ScaleDown already handled it
		if IsScaledDownByOperator(d) {
			continue
		}

		// Skip if already at 0 for an unrelated reason — don't own what isn't ours
		if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 {
			continue
		}

		w := idle.IdleWorkload{
			Name:      d.Name,
			Namespace: d.Namespace,
		}
		if err := ScaleDown(ctx, c, w); err != nil {
			// log and continue — don't let one failure block the rest
			fmt.Printf("failed to suspend %s: %v\n", d.Name, err)
		}
		suspended = append(suspended, d.Name)
	}

	return suspended, nil
}
