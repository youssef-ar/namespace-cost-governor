package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/youssef-ar/namespace-cost-governor/internal/actions"
)

func restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <namespace>",
		Short: "Restore all workloads scaled down by the cost governor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			namespace, err := validateNamespace(args[0])
			if err != nil {
				return err
			}

			c, err := buildClient()
			if err != nil {
				return fmt.Errorf("building client: %w", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// List all deployments in the namespace
			deploymentList := &appsv1.DeploymentList{}
			if err := c.List(ctx, deploymentList,
				client.InNamespace(namespace)); err != nil {
				return fmt.Errorf("listing deployments: %w", err)
			}

			restored := 0
			for _, d := range deploymentList.Items {
				// Only restore deployments scaled down by the operator
				if !actions.IsScaledDownByOperator(d) {
					continue
				}

				originalStr, ok := d.Annotations[actions.AnnotationOriginalReplicas]
				if !ok {
					fmt.Printf("  skipping %s: missing original-replicas annotation\n", d.Name)
					continue
				}
				original, err := strconv.ParseInt(originalStr, 10, 32)
				if err != nil {
					fmt.Printf("  skipping %s: invalid original-replicas annotation\n", d.Name)
					continue
				}

				replicas := int32(original)
				patch := client.MergeFrom(d.DeepCopy())
				d.Spec.Replicas = &replicas
				// Remove cost annotations
				delete(d.Annotations, actions.AnnotationScaledDownBy)
				delete(d.Annotations, actions.AnnotationScaledDownAt)
				delete(d.Annotations, actions.AnnotationOriginalReplicas)
				delete(d.Annotations, actions.AnnotationReason)

				if err := c.Patch(ctx, &d, patch); err != nil {
					fmt.Printf("  failed to restore %s: %v\n", d.Name, err)
					continue
				}

				fmt.Printf("  restored %s → %d replicas\n", d.Name, replicas)
				restored++
			}

			if restored == 0 {
				fmt.Println("No workloads to restore.")
			} else {
				fmt.Printf("\nRestored %d workload(s).\n", restored)
			}

			return nil
		},
	}
}
