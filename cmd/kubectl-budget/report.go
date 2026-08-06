package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
)

func reportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report <namespace>",
		Short: "Print the latest cost report for a namespace",
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

			// List all CostReports in the namespace
			reportList := &costv1alpha1.CostReportList{}
			if err := c.List(ctx, reportList,
				client.InNamespace(namespace)); err != nil {
				return fmt.Errorf("listing cost reports: %w", err)
			}

			if len(reportList.Items) == 0 {
				fmt.Printf("No cost reports found for namespace %s.\n", namespace)
				return nil
			}

			// Sort by period descending — pick the latest
			sort.Slice(reportList.Items, func(i, j int) bool {
				return reportList.Items[i].Status.Period > reportList.Items[j].Status.Period
			})
			report := reportList.Items[0]

			// Print header
			fmt.Printf("Cost Report — %s (%s)\n", namespace, report.Status.Period)
			fmt.Println(strings.Repeat("─", 50))

			// Total cost
			fmt.Printf("Total CPU:    %s core-hours\n", report.Status.TotalCost.CoreHours)
			fmt.Printf("Total Memory: %s GiB-hours\n", report.Status.TotalCost.GiBHours)
			fmt.Printf("Estimated:    %s\n", report.Status.TotalCost.EstimatedUSD)
			fmt.Println()

			// Top consumers
			if len(report.Status.TopConsumers) > 0 {
				fmt.Println("Top consumers:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintf(w, "  WORKLOAD\tCPU%%\tMEM%%\tESTIMATED\n")
				for _, tc := range report.Status.TopConsumers {
					fmt.Fprintf(w, "  %s\t%d%%\t%d%%\t%s\n",
						tc.Workload, tc.CPUPercent, tc.MemoryPercent, tc.EstimatedUSD,
					)
				}
				w.Flush()
				fmt.Println()
			}

			// Suspension events
			if len(report.Status.SuspendedEvents) > 0 {
				fmt.Println("Suspension events:")
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
				fmt.Fprintf(w, "  WORKLOAD\tSCALED DOWN AT\tREASON\n")
				for _, ev := range report.Status.SuspendedEvents {
					fmt.Fprintf(w, "  %s\t%s\t%s\n",
						ev.Workload, ev.ScaledDownAt, ev.Reason,
					)
				}
				w.Flush()
			}

			return nil
		},
	}
}
