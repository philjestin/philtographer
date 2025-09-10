package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/philjestin/philtographer/internal/scan"
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan the workspace and output the dependency graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Pull merged values (flags > env > config > defaults)
		root := viper.GetString("root")
		out := viper.GetString("out")

		// ctx lets us cancel a long walk
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// Build the full-graph (walk entire tree). For multi-root entry-driven scanning,
		// call scan.BuildGraphFromEntries instead (wired in a separate subcommand later).
		g, err := scan.BuildGraph(ctx, root)
		if err != nil {
			return err
		}

		// Write to file or stdout (same output logic you had before).
		var enc *json.Encoder
		if out != "" {
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			enc = json.NewEncoder(f)
			enc.SetIndent("", "  ")
			if err := enc.Encode(g); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", out)
			return nil
		}

		enc = json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(g)
	},
}

func init() {
	rootCmd.AddCommand(scanCmd)
}
