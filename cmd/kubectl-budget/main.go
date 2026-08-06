package main

import (
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "kubectl-budget",
		Short: "Manage namespace cost budgets",
	}

	rootCmd.AddCommand(statusCmd())
	rootCmd.AddCommand(restoreCmd())
	rootCmd.AddCommand(reportCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
