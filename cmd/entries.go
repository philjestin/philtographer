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
)

// entriesCmd builds a graph by first discovering roots via providers specified in config.
// It targets the "multi root / MPA" use case (e.g., Rails + many React roots).
var entriesCmd = &cobra.Command{
	Use:   "entries",
	Short: "Discover entry points from config (e.g., roots.ts) and build the graph from them",
	RunE: func(cmd *cobra.Command, args []string) error {
		// 1) Read merged config (flags > env > config). We rely on viper pre-run set in root.go.
		var cfg scan.Config
		if err := viper.Unmarshal(&cfg); err != nil {
			return fmt.Errorf("config unmarshal: %w", err)
		}
		if cfg.Root == "" {
			cfg.Root = "." // default fallback
		}
		out := viper.GetString("out")
		if out == "" && cfg.Out != "" {
			out = cfg.Out
		}

		// 2) Build providers from cfg. For now: rootsTs, explicit. Extend here as you add more types.
		var provs []providers.Provider
		for _, spec := range cfg.Entries {
			switch spec.Type {
			case "rootsTs":
				provs = append(provs, providers.RootsTsProvider{
					File:     spec.File,
					NameFrom: spec.NameFrom, // "objectKey" | "webpackChunkName"
				})
			case "explicit":
				provs = append(provs, providers.ExplicitProvider{
					Name: spec.Name,
					Path: spec.Path,
				})
			default:
				return fmt.Errorf("unknown entry provider type: %s", spec.Type)
			}
		}

		// 3) Run providers and de-duplicate entries by absolute path.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		seen := map[string]bool{}
		var entries []scan.Entry
		for _, p := range provs {
			es, err := p.Discover(ctx, cfg.Root)
			if err != nil {
				return err
			}
			for _, e := range es {
				if !seen[e.Path] {
					seen[e.Path] = true
					entries = append(entries, e)
				}
			}
		}
		if len(entries) == 0 {
			return fmt.Errorf("no entries discovered; check your config")
		}

		// 4) Build graph from discovered entries (closure over reachable files only).
		g, err := scan.BuildGraphFromEntries(ctx, cfg.Root, entries)
		if err != nil {
			return err
		}

		// 5) Persist to file or stdout, same as scan.
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
	rootCmd.AddCommand(entriesCmd)
}
