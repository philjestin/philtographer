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
	"github.com/philjestin/philtographer/internal/scan/providers"
	"github.com/philjestin/philtographer/internal/tsgraph"
)

var componentsCmd = &cobra.Command{
	Use:   "components",
	Short: "Build a React component graph (TSX) using tree-sitter and output JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg scan.Config
		if err := viper.Unmarshal(&cfg); err != nil {
			return fmt.Errorf("config unmarshal: %w", err)
		}
		if cfg.Root == "" {
			cfg.Root = "."
		}
		out := viper.GetString("out")
		if out == "" && cfg.Out != "" {
			out = cfg.Out
		}

		if len(cfg.Entries) == 0 {
			return fmt.Errorf("no entries configured; components scan requires entries")
		}

		// Build providers from config (reuse logic from entries command)
		var provs []providers.Provider
		for _, spec := range cfg.Entries {
			switch spec.Type {
			case "rootsTs":
				provs = append(provs, providers.RootsTsProvider{File: spec.File, NameFrom: spec.NameFrom})
			case "explicit":
				provs = append(provs, providers.ExplicitProvider{Name: spec.Name, Path: spec.Path})
			default:
				return fmt.Errorf("unknown entry provider type: %s", spec.Type)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		seen := map[string]bool{}
		var entryPaths []string
		for _, p := range provs {
			es, err := p.Discover(ctx, cfg.Root)
			if err != nil {
				return err
			}
			for _, e := range es {
				if !seen[e.Path] {
					seen[e.Path] = true
					entryPaths = append(entryPaths, e.Path)
				}
			}
		}
		if len(entryPaths) == 0 {
			return fmt.Errorf("no entry paths resolved from config entries")
		}

		// progress printer (rate-limited, single line)
		var last time.Time
		progress := func(visited, edges, queued int) {
			now := time.Now()
			if now.Sub(last) < 200*time.Millisecond {
				return
			}
			last = now
			fmt.Fprintf(os.Stderr, "\rcomponents: visited=%d edges=%d queued=%d", visited, edges, queued)
		}

		g, err := tsgraph.BuildComponentGraphFromEntriesProgress(ctx, cfg.Root, entryPaths, progress)
		// finish the progress line
		fmt.Fprintln(os.Stderr)
		if err != nil && err != context.Canceled {
			return err
		}

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

func init() { rootCmd.AddCommand(componentsCmd) }
