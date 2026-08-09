package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <namespace>",
		Short: "Show budget status for a namespace",
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

			budget := &costv1alpha1.NamespaceBudget{}
			if err := c.Get(ctx, types.NamespacedName{
				Name:      "budget",
				Namespace: namespace,
			}, budget); err != nil {
				return fmt.Errorf("fetching budget for namespace %s: %w", namespace, err)
			}

			// Print summary table
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			if _, err := fmt.Fprintf(w, "NAMESPACE\tPHASE\tBUDGET%%\tCPU\t MEMORY\tLAST RECONCILE\n"); err != nil {
				return fmt.Errorf("writing status header: %w", err)
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%d%%\t%s cores\t%s GiB\t%s\n",
				namespace,
				budget.Status.Phase,
				budget.Status.BudgetPercent,
				budget.Status.CurrentUsage.Cpu,
				budget.Status.CurrentUsage.Memory,
				budget.Status.LastReconcile.Format("2006-01-02 15:04:05"),
			); err != nil {
				return fmt.Errorf("writing status: %w", err)
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("flushing status: %w", err)
			}

			// Print idle workloads if any
			if len(budget.Status.IdleWorkloads) > 0 {
				fmt.Println("\nIdle workloads:")
				w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				if _, err := fmt.Fprintf(w2, "  NAME\tIDLE SINCE\n"); err != nil {
					return fmt.Errorf("writing idle header: %w", err)
				}
				for _, idle := range budget.Status.IdleWorkloads {
					if _, err := fmt.Fprintf(w2, "  %s\t%s\n",
						idle.Name,
						idle.IdleSince.Format("2006-01-02 15:04:05"),
					); err != nil {
						return fmt.Errorf("writing idle workload: %w", err)
					}
				}
				if err := w2.Flush(); err != nil {
					return fmt.Errorf("flushing idle workloads: %w", err)
				}
			}

			// Print conditions
			if len(budget.Status.Conditions) > 0 {
				fmt.Println("\nConditions:")
				w3 := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				if _, err := fmt.Fprintf(w3, "  TYPE\tSTATUS\tMESSAGE\n"); err != nil {
					return fmt.Errorf("writing conditions header: %w", err)
				}
				for _, c := range budget.Status.Conditions {
					if _, err := fmt.Fprintf(w3, "  %s\t%s\t%s\n",
						c.Type, c.Status, c.Message,
					); err != nil {
						return fmt.Errorf("writing condition: %w", err)
					}
				}
				if err := w3.Flush(); err != nil {
					return fmt.Errorf("flushing conditions: %w", err)
				}
			}

			return nil
		},
	}
}
