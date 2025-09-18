package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/philjestin/philtographer/internal/graph"
	"github.com/philjestin/philtographer/internal/scan"
	"github.com/philjestin/philtographer/internal/scan/providers"
	"github.com/philjestin/philtographer/internal/tsgraph"
)

var (
	watchMode   string // "scan" or "components"
	watchGraph  string // file to write graph json
	watchEvents string // file to write events json (changed + impacted)
    watchAffectedOnly bool // if true, write only affected subgraph to --graph after changes
)

// watchCmd watches the workspace and rebuilds the graph on changes, emitting impacted sets.
var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch source files, rebuild the graph, and emit impacted nodes",
	RunE: func(cmd *cobra.Command, args []string) error {
		if watchGraph == "" {
			return fmt.Errorf("--graph is required (output graph.json path)")
		}
		// Assemble config
		var cfg scan.Config
		if err := viper.Unmarshal(&cfg); err != nil {
			return fmt.Errorf("config unmarshal: %w", err)
		}
		if cfg.Root == "" {
			cfg.Root = "."
		}
		if abs, err := filepath.Abs(cfg.Root); err == nil {
			cfg.Root = filepath.Clean(abs)
		}
		if watchEvents == "" {
			watchEvents = filepath.Join(filepath.Dir(watchGraph), "events.json")
		}

		build := func(ctx context.Context, changed []string) (*graph.Graph, []string, error) {
			switch watchMode {
			case "components":
				// collect entry paths similar to components command
				var provs []providers.Provider
				for _, spec := range cfg.Entries {
					switch spec.Type {
					case "rootsTs":
						provs = append(provs, providers.RootsTsProvider{File: spec.File, NameFrom: spec.NameFrom})
					case "explicit":
						provs = append(provs, providers.ExplicitProvider{Name: spec.Name, Path: spec.Path})
					default:
						return nil, nil, fmt.Errorf("unknown entry provider type: %s", spec.Type)
					}
				}
				seen := map[string]bool{}
				var entryPaths []string
				for _, p := range provs {
					es, err := p.Discover(ctx, cfg.Root)
					if err != nil {
						return nil, nil, err
					}
					for _, e := range es {
						if !seen[e.Path] {
							seen[e.Path] = true
							entryPaths = append(entryPaths, e.Path)
						}
					}
				}
				if len(entryPaths) == 0 {
					// fallback: try root/index.*
					rp := cfg.Root
					if fi, err := os.Stat(rp); err == nil && fi.IsDir() {
						for _, name := range []string{"index.tsx", "index.ts", "index.jsx", "index.js"} {
							cand := filepath.Join(rp, name)
							if info, err := os.Stat(cand); err == nil && !info.IsDir() {
								rp = cand
								break
							}
						}
					}
					entryPaths = []string{rp}
				}
				g, err := tsgraph.BuildComponentGraphFromEntries(context.Background(), cfg.Root, entryPaths)
				if err != nil && !errors.Is(err, context.Canceled) {
					return g, nil, err
				}
				return g, impactedForChanges(cfg.Root, g, changed), nil
			default:
				g, err := scan.BuildGraph(context.Background(), cfg.Root)
				if err != nil && !errors.Is(err, context.Canceled) {
					return g, nil, err
				}
				return g, impactedForChanges(cfg.Root, g, changed), nil
			}
		}

        // initial build (write full graph)
        if err := doRebuild(cfg.Root, build, watchGraph, watchEvents, nil, false); err != nil {
			return err
		}

		// watcher setup
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer watcher.Close()

		// add directories recursively
		if err := addRecursive(watcher, cfg.Root); err != nil {
			return err
		}

		// debounce changes
		var mu sync.Mutex
		pending := map[string]struct{}{}
		var timer *time.Timer
		flush := func() {
			mu.Lock()
			files := make([]string, 0, len(pending))
			for f := range pending {
				files = append(files, f)
			}
			pending = map[string]struct{}{}
            mu.Unlock()
            _ = doRebuild(cfg.Root, build, watchGraph, watchEvents, files, watchAffectedOnly)
		}

		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return nil
				}
				// track new directories
				if ev.Op&fsnotify.Create == fsnotify.Create {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						_ = addRecursive(watcher, ev.Name)
						continue
					}
				}
				// only care about file changes with code extensions
				if isWatchedFile(ev.Name) {
					mu.Lock()
					p := ev.Name
					if !filepath.IsAbs(p) {
						if a, err := filepath.Abs(p); err == nil {
							p = a
						}
					}
					pending[filepath.Clean(p)] = struct{}{}
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(300*time.Millisecond, flush)
					mu.Unlock()
				}
			case err := <-watcher.Errors:
				fmt.Fprintln(os.Stderr, "watch error:", err)
			}
		}
	},
}

func isWatchedFile(p string) bool {
	l := strings.ToLower(p)
	return strings.HasSuffix(l, ".ts") || strings.HasSuffix(l, ".tsx") || strings.HasSuffix(l, ".js") || strings.HasSuffix(l, ".jsx") || strings.HasSuffix(l, ".d.ts")
}

func addRecursive(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" {
				if path != root {
					return filepath.SkipDir
				}
				return nil
			}
			_ = w.Add(path)
		}
		return nil
	})
}

// filterSubgraph returns a JSON-serializable view of only nodes in keep and edges among them.
func filterSubgraph(g *graph.Graph, keep map[string]bool) interface{} {
    // Collect nodes
    nodes := []string{}
    for n := range keep { nodes = append(nodes, n) }
    type edge struct{ From, To string }
    edges := []edge{}
    g.ForEachEdge(func(from, to string) {
        if keep[from] && keep[to] { edges = append(edges, edge{From: from, To: to}) }
    })
    return struct {
        Nodes []string `json:"nodes"`
        Edges []edge   `json:"edges"`
    }{Nodes: nodes, Edges: edges}
}

func doRebuild(root string, build func(context.Context, []string) (*graph.Graph, []string, error), outGraph, outEvents string, changed []string, affectedOnly bool) error {
	g, impacted, err := build(context.Background(), changed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build error:", err)
	}
	if g != nil {
        // If requested, write only the subgraph for changed+impacted (after changes).
        if affectedOnly && len(changed) > 0 {
            keep := map[string]bool{}
            for _, c := range changed { keep[filepath.Clean(c)] = true }
            for _, i := range impacted { keep[filepath.Clean(i)] = true }
            sg := filterSubgraph(g, keep)
            if err := writeJSONFile(outGraph, sg); err != nil { fmt.Fprintln(os.Stderr, "write graph:", err) }
        } else {
            if err := writeJSONFile(outGraph, g); err != nil {
                fmt.Fprintln(os.Stderr, "write graph:", err)
            }
        }
	}
	// write events JSON even if graph failed; impacted may be empty
	evt := struct {
		Timestamp int64    `json:"ts"`
		Changed   []string `json:"changed"`
		Impacted  []string `json:"impacted"`
	}{Timestamp: time.Now().UnixMilli(), Changed: changed, Impacted: impacted}
	if err := writeJSONFile(outEvents, evt); err != nil {
		fmt.Fprintln(os.Stderr, "write events:", err)
	}
	return nil
}

func impactedForChanges(root string, g *graph.Graph, changed []string) []string {
	if g == nil || len(changed) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, c := range changed {
		// normalize to absolute, then to cleaned path used in nodes
		if !filepath.IsAbs(c) {
			if a, err := filepath.Abs(filepath.Join(root, c)); err == nil {
				c = a
			}
		}
		c = filepath.Clean(c)
		for _, imp := range g.Impacted(c) {
			if _, ok := seen[imp]; ok {
				continue
			}
			seen[imp] = struct{}{}
			out = append(out, imp)
		}
	}
	return out
}

func writeJSONFile(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().StringVar(&watchMode, "mode", "scan", "build mode: scan|components")
	watchCmd.Flags().StringVar(&watchGraph, "graph", "", "output graph.json path")
	watchCmd.Flags().StringVar(&watchEvents, "events", "", "output events.json path (default: sibling of --graph)")
    watchCmd.Flags().BoolVar(&watchAffectedOnly, "affected-only", false, "write only affected subgraph to --graph after each change")
}
