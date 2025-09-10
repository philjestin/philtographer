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

// CLI flags (local to this subcommand)
var (
	printEntries bool // if true, list discovered entries then exit (no graph build)
	verbose      bool // if true, print extra diagnostics to stderr
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

		if verbose {
			fmt.Fprintln(os.Stderr, "[entries] root =", cfg.Root, "out =", out)
			fmt.Fprintln(os.Stderr, "[entries] provider specs =", len(cfg.Entries))
		}

		// 2) Build providers from cfg. For now: rootsTs, explicit. Extend here as you add more types.
		var provs []providers.Provider
		for _, spec := range cfg.Entries {
			switch spec.Type {
			case "rootsTs":
				if verbose {
					fmt.Fprintln(os.Stderr, "[entries] add rootsTs provider file:", spec.File, "nameFrom:", spec.NameFrom)
				}
				provs = append(provs, providers.RootsTsProvider{
					File:     spec.File,
					NameFrom: spec.NameFrom, // "objectKey" | "webpackChunkName"
				})
			case "explicit":
				if verbose {
					fmt.Fprintln(os.Stderr, "[entries] add explicit provider", spec.Name, "->", spec.Path)
				}
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

		if verbose {
			fmt.Fprintln(os.Stderr, "[entries] discovered entries:", len(entries))
		}

		// If --print-entries is on, list them to stderr and exit early.
		if printEntries {
			for _, e := range entries {
				fmt.Fprintf(os.Stderr, "• %s  %s\n", e.Name, e.Path)
			}
			// Early return: don't build the graph.
			return nil
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
	// Register subcommand and its flags.
	rootCmd.AddCommand(entriesCmd)
	entriesCmd.Flags().BoolVar(&printEntries, "print-entries", false, "print discovered entries and exit")
	entriesCmd.Flags().BoolVar(&verbose, "verbose", false, "verbose logging (providers, matches, paths)")
}
