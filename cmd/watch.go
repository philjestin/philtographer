package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
	watchMode         string // "scan" or "components"
	watchGraph        string // file to write graph json
	watchEvents       string // file to write events json (changed + impacted)
	watchAffectedOnly bool   // if true, write only affected subgraph to --graph after changes
	watchIncludeDeps  bool   // if true, include forward transitive deps from importer seeds
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

		// No polling mode - always use event-driven watching
		fmt.Fprintf(os.Stderr, "[watch] zero-polling mode enabled\n")

		// watcher setup (fsnotify)
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return err
		}
		defer watcher.Close()

		// Prefer selective recursive watching using tsconfig baseUrl and paths targets under monorepo root
		resolver := scan.NewResolver(cfg.Root)
		aliasDirs := resolver.WatchDirs()
		// If user provided explicit watch dirs in config, prefer those
		if len(cfg.WatchDirs) > 0 {
			aliasDirs = []string{}
			for _, wd := range cfg.WatchDirs {
				p := wd
				if !filepath.IsAbs(p) {
					p = filepath.Clean(filepath.Join(cfg.Root, p))
				}
				aliasDirs = append(aliasDirs, p)
			}
		}
		// filter out root to avoid massive fanout
		filtered := []string{}
		for _, d := range aliasDirs {
			if filepath.Clean(d) == filepath.Clean(cfg.Root) {
				continue
			}
			filtered = append(filtered, d)
		}
		if len(filtered) == 0 {
			filtered = []string{cfg.Root}
		}
		watchCount := 0
		var addErr error
		for _, d := range filtered {
			c, e := addRecursiveWithCount(watcher, d)
			watchCount += c
			if e != nil {
				addErr = e
				break
			}
		}
		if addErr != nil {
			// If we hit system limits or our own max-dir limit, switch to hierarchical watching
			low := strings.ToLower(addErr.Error())
			if strings.Contains(low, "too many open files") || strings.Contains(low, "hit watch limit") {
				fmt.Fprintf(os.Stderr, "[watch] hit watch limit at %d dirs, switching to hierarchical mode\n", watchCount)
				watcher.Close()
				return hierarchicalWatch(cfg.Root, build, watchGraph, watchEvents)
			}
			return addErr
		}
		fmt.Fprintf(os.Stderr, "[watch] watching %d directories with zero-polling mode\n", watchCount)

		// Advanced debouncing and coalescing for large repos
		var mu sync.Mutex
		pending := map[string]struct{}{}
		var timer *time.Timer
		var rebuildMu sync.Mutex

		// Event pattern detection for micro-delays (zero polling approach)
		var eventHistory []time.Time
		var lastEventTime time.Time

		// Determine optimal flush delay based on real-time event patterns
		getOptimalDelay := func(changedFiles []string, eventGap time.Duration) time.Duration {
			// Single file edit: near-immediate (filesystem settle time only)
			if len(changedFiles) == 1 && eventGap > 100*time.Millisecond {
				return 10 * time.Millisecond
			}

			// Rapid burst detected: wait for it to complete
			if eventGap < 50*time.Millisecond {
				return 100 * time.Millisecond
			}

			// Analyze recent event patterns for bulk operations
			if len(eventHistory) >= 3 {
				recent := eventHistory[len(eventHistory)-3:]
				if recent[2].Sub(recent[0]) < 500*time.Millisecond {
					// Git checkout, mass save, etc - wait for stabilization
					return 500 * time.Millisecond
				}
			}

			// Small batch of changes: minimal delay
			if len(changedFiles) <= 5 {
				return 50 * time.Millisecond
			}

			// Large batch: allow completion
			return 1 * time.Second
		}

		flush := func() {
			mu.Lock()
			files := make([]string, 0, len(pending))
			for f := range pending {
				files = append(files, f)
			}
			pending = map[string]struct{}{}
			mu.Unlock()

			if len(files) == 0 {
				return
			}

			// Serialize rebuilds to prevent overlapping builds
			rebuildMu.Lock()
			fmt.Fprintf(os.Stderr, "[watch] rebuilding graph for %d changed files\n", len(files))
			_ = doRebuild(cfg.Root, build, watchGraph, watchEvents, files, watchAffectedOnly)
			rebuildMu.Unlock()
		}

		// Intelligent scheduling: analyze event patterns in real-time
		scheduleFlush := func() {
			mu.Lock()
			files := make([]string, 0, len(pending))
			for f := range pending {
				files = append(files, f)
			}

			now := time.Now()
			eventGap := now.Sub(lastEventTime)

			// Track event history for pattern detection (rolling window)
			eventHistory = append(eventHistory, now)
			if len(eventHistory) > 10 {
				eventHistory = eventHistory[1:]
			}
			lastEventTime = now
			mu.Unlock()

			delay := getOptimalDelay(files, eventGap)

			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(delay, flush)
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
					mu.Unlock()

					// Trigger intelligent scheduling based on event patterns
					scheduleFlush()
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

// addRecursiveWithCount adds directories to watcher with large-repo optimizations, returns watch count
func addRecursiveWithCount(w *fsnotify.Watcher, root string) (int, error) {
	// For very large repos, limit depth and watch strategically
	// Adaptive default: try a higher safe ceiling first, fall back to hierarchical if hit
	maxWatchDirs := 4000
	if env := os.Getenv("PHILTOGRAPHER_MAX_WATCH_DIRS"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			maxWatchDirs = n
		}
	}

	watchCount := 0
	var walkErr error

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Continue on individual file errors
		}
		if d.IsDir() {
			name := d.Name()
			// Skip common build/cache/vendor dirs (configurable via ignore patterns)
			skipDirs := []string{
				"node_modules", "dist", "build", ".next", ".nuxt", "coverage",
				"tmp", "temp", ".cache", "vendor", ".git", "__pycache__",
				".pytest_cache", ".tox", ".venv", "venv", ".mypy_cache",
			}
			for _, skip := range skipDirs {
				if name == skip {
					if path != root {
						return filepath.SkipDir
					}
					return nil
				}
			}
			// Skip deep hidden dirs except at root level
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}

			// Add directory to watcher if under limit
			if watchCount < maxWatchDirs {
				if err := w.Add(path); err != nil {
					// If this specific Add fails, try to continue but record the error
					if strings.Contains(strings.ToLower(err.Error()), "too many open files") {
						walkErr = fmt.Errorf("hit watch limit at %d directories: %w", watchCount, err)
						return filepath.SkipDir
					}
					// Other errors - continue but don't count this dir
				} else {
					watchCount++
				}
			} else {
				// Hit watch limit - record and stop walking deeper
				fmt.Fprintf(os.Stderr, "[watch] hit %d directory limit at %s\n", maxWatchDirs, path)
				if walkErr == nil {
					walkErr = fmt.Errorf("hit watch limit at %d directories", watchCount)
				}
				return filepath.SkipDir
			}
		}
		return nil
	})

	if walkErr != nil {
		return watchCount, walkErr
	}
	return watchCount, err
}

// Legacy wrapper for compatibility
func addRecursive(w *fsnotify.Watcher, root string) error {
	_, err := addRecursiveWithCount(w, root)
	return err
}

// filterSubgraph returns a JSON-serializable view of only nodes in keep and edges among them.
func filterSubgraph(g *graph.Graph, keep map[string]bool) interface{} {
	// Collect nodes
	nodes := []string{}
	for n := range keep {
		nodes = append(nodes, n)
	}
	type edge struct{ From, To string }
	edges := []edge{}
	// Build a set for deduplication and a simple adjacency for 2-hop flattening
	edgeSet := map[string]map[string]bool{}
	g.ForEachEdge(func(from, to string) {
		if keep[from] && keep[to] {
			if _, ok := edgeSet[from]; !ok {
				edgeSet[from] = map[string]bool{}
			}
			if !edgeSet[from][to] {
				edgeSet[from][to] = true
				edges = append(edges, edge{From: from, To: to})
			}
		}
	})

	// Flatten simple barrel paths inside the subgraph:
	// for each from->mid and mid->to (all within keep), add a synthetic from->to edge.
	for from, mids := range edgeSet {
		for mid := range mids {
			tos, ok := edgeSet[mid]
			if !ok {
				continue
			}
			for to := range tos {
				if from == to {
					continue
				}
				if _, ok := edgeSet[from]; !ok {
					edgeSet[from] = map[string]bool{}
				}
				if edgeSet[from][to] {
					continue
				}
				edgeSet[from][to] = true
				edges = append(edges, edge{From: from, To: to})
			}
		}
	}
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
			for _, c := range changed {
				keep[filepath.Clean(c)] = true
			}
			for _, i := range impacted {
				keep[filepath.Clean(i)] = true
			}
			sg := filterSubgraph(g, keep)
			if err := writeJSONFile(outGraph, sg); err != nil {
				fmt.Fprintln(os.Stderr, "write graph:", err)
			} else {
				fmt.Fprintf(os.Stderr, "[watch] wrote affected graph: changed=%d impacted=%d\n", len(changed), len(impacted))
			}
		} else {
			if err := writeJSONFile(outGraph, g); err != nil {
				fmt.Fprintln(os.Stderr, "write graph:", err)
			} else {
				fmt.Fprintf(os.Stderr, "[watch] wrote full graph: nodes=%d\n", len(g.Nodes()))
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
	} else {
		fmt.Fprintf(os.Stderr, "[watch] events updated (changed=%d impacted=%d)\n", len(changed), len(impacted))
	}
	return nil
}

func impactedForChanges(root string, g *graph.Graph, changed []string) []string {
	if g == nil || len(changed) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	// Build a quick set of node keys for fallback matching
	nodes := g.Nodes()
	for _, c := range changed {
		// normalize to absolute, then to cleaned path used in nodes
		if !filepath.IsAbs(c) {
			if a, err := filepath.Abs(filepath.Join(root, c)); err == nil {
				c = a
			}
		}
		// resolve symlinks if possible to match how nodes were recorded
		if real, err := filepath.EvalSymlinks(c); err == nil {
			c = real
		}
		c = filepath.Clean(c)

		// Seed with direct importers (incoming edges)
		impacted := g.InNeighbors(c)
		// Also include the changed file's immediate outgoing deps (to capture barrel targets)
		impacted = append(impacted, g.OutNeighbors(c)...)
		// Fallbacks: try alternate extensions if no impacted found
		if len(impacted) == 0 {
			if strings.HasSuffix(c, ".ts") {
				impacted = g.InNeighbors(strings.TrimSuffix(c, ".ts") + ".tsx")
			}
			if strings.HasSuffix(c, ".tsx") && len(impacted) == 0 {
				impacted = g.InNeighbors(strings.TrimSuffix(c, ".tsx") + ".ts")
			}
		}
		// Barrel expansion (fallback only): if this is an index.* and no direct importers were found,
		// include importers of its direct re-export targets to approximate impact.
		if len(impacted) == 0 {
			base := filepath.Base(c)
			if base == "index.ts" || base == "index.tsx" || base == "index.js" || base == "index.jsx" {
				deps := []string{}
				g.ForEachEdge(func(from, to string) {
					if from == c {
						deps = append(deps, to)
					}
				})
				for _, d := range deps {
					more := g.InNeighbors(d)
					if len(more) > 0 {
						impacted = append(impacted, more...)
					}
				}
			}
		}

		// Optionally include forward closure for context
		if watchIncludeDeps {
			q := []string{}
			inKeep := map[string]bool{}
			for _, n := range impacted {
				if strings.HasPrefix(n, "pkg:") {
					continue
				}
				if !inKeep[n] {
					inKeep[n] = true
					q = append(q, n)
				}
			}
			for len(q) > 0 {
				n := q[0]
				q = q[1:]
				for _, dep := range g.OutNeighbors(n) {
					if strings.HasPrefix(dep, "pkg:") {
						continue
					}
					if !inKeep[dep] {
						inKeep[dep] = true
						q = append(q, dep)
					}
				}
			}
			// Merge forward closure into impacted list
			for n := range inKeep {
				impacted = append(impacted, n)
			}
		}
		// Last resort: suffix match to a node key
		if len(impacted) == 0 {
			base := filepath.Base(c)
			best := ""
			for _, n := range nodes {
				if filepath.Base(n) == base && strings.HasSuffix(n, base) {
					best = n
					break
				}
			}
			if best != "" {
				impacted = g.Impacted(best)
			}
		}

		for _, imp := range impacted {
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
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// ensure cleanup on failure
	defer func() { _ = os.Remove(tmpPath) }()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// hierarchicalWatch implements a hierarchical watching strategy for extremely large repos
func hierarchicalWatch(root string, build func(context.Context, []string) (*graph.Graph, []string, error), outGraph, outEvents string) error {
	fmt.Fprintf(os.Stderr, "[watch] starting hierarchical watch mode for large repo\n")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch top-level source directories + tsconfig alias targets
	criticalDirs := []string{}
	// discover immediate children in root to avoid deep fanout
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if name == "node_modules" || name == "dist" || name == "build" || name == ".git" || name == "coverage" {
				continue
			}
			criticalDirs = append(criticalDirs, filepath.Join(root, name))
		}
	}

	watchCount := 0
	for _, dir := range criticalDirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			// Watch top-level only, rely on CREATE events for deeper dirs
			if err := watcher.Add(dir); err == nil {
				watchCount++
				fmt.Fprintf(os.Stderr, "[watch] watching critical dir: %s\n", dir)
			}
		}
	}

	// Always watch root for new directories
	_ = watcher.Add(root)
	watchCount++

	// Add tsconfig alias directories
	aliasDirs := scan.NewResolver(root).WatchDirs()
	for _, d := range aliasDirs {
		if err := watcher.Add(d); err == nil {
			watchCount++
		}
	}

	fmt.Fprintf(os.Stderr, "[watch] hierarchical mode watching %d directories\n", watchCount)

	// Use the same intelligent event batching and also subscribe to new directories dynamically
	var mu sync.Mutex
	pending := map[string]struct{}{}
	var timer *time.Timer
	var rebuildMu sync.Mutex
	var eventHistory []time.Time
	var lastEventTime time.Time

	getOptimalDelay := func(changedFiles []string, eventGap time.Duration) time.Duration {
		if len(changedFiles) == 1 && eventGap > 100*time.Millisecond {
			return 10 * time.Millisecond
		}
		if eventGap < 50*time.Millisecond {
			return 100 * time.Millisecond
		}
		if len(eventHistory) >= 3 {
			recent := eventHistory[len(eventHistory)-3:]
			if recent[2].Sub(recent[0]) < 500*time.Millisecond {
				return 500 * time.Millisecond
			}
		}
		if len(changedFiles) <= 5 {
			return 50 * time.Millisecond
		}
		return 1 * time.Second
	}

	flush := func() {
		mu.Lock()
		files := make([]string, 0, len(pending))
		for f := range pending {
			files = append(files, f)
		}
		pending = map[string]struct{}{}
		mu.Unlock()

		if len(files) == 0 {
			return
		}

		rebuildMu.Lock()
		fmt.Fprintf(os.Stderr, "[watch] hierarchical rebuild for %d changed files\n", len(files))
		_ = doRebuild(root, build, outGraph, outEvents, files, watchAffectedOnly)
		rebuildMu.Unlock()
	}

	scheduleFlush := func() {
		mu.Lock()
		files := make([]string, 0, len(pending))
		for f := range pending {
			files = append(files, f)
		}

		now := time.Now()
		eventGap := now.Sub(lastEventTime)

		eventHistory = append(eventHistory, now)
		if len(eventHistory) > 10 {
			eventHistory = eventHistory[1:]
		}
		lastEventTime = now
		mu.Unlock()

		delay := getOptimalDelay(files, eventGap)

		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(delay, flush)
	}

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// Auto-watch new directories that are created
			if ev.Op&fsnotify.Create == fsnotify.Create {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					// Only watch if it's not a build/dist dir
					name := filepath.Base(ev.Name)
					if !strings.HasPrefix(name, ".") && name != "node_modules" &&
						name != "dist" && name != "build" {
						_ = watcher.Add(ev.Name)
					}
					continue
				}
			}
			// Process file changes
			if isWatchedFile(ev.Name) {
				mu.Lock()
				p := ev.Name
				if !filepath.IsAbs(p) {
					if a, err := filepath.Abs(p); err == nil {
						p = a
					}
				}
				pending[filepath.Clean(p)] = struct{}{}
				mu.Unlock()
				scheduleFlush()
			}
		case err := <-watcher.Errors:
			fmt.Fprintln(os.Stderr, "hierarchical watch error:", err)
		}
	}
}

func init() {
	rootCmd.AddCommand(watchCmd)
	watchCmd.Flags().StringVar(&watchMode, "mode", "scan", "build mode: scan|components")
	watchCmd.Flags().StringVar(&watchGraph, "graph", "", "output graph.json path")
	watchCmd.Flags().StringVar(&watchEvents, "events", "", "output events.json path (default: sibling of --graph)")
	watchCmd.Flags().BoolVar(&watchAffectedOnly, "affected-only", false, "write only affected subgraph to --graph after each change")
	// Polling removed - zero-polling mode always enabled
	watchCmd.Flags().BoolVar(&watchIncludeDeps, "include-deps", false, "include forward transitive dependencies from importer seeds in impacted set")
}
