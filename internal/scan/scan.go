package scan

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/philjestin/philtographer/internal/graph"
)

var (
	reImportFrom = regexp.MustCompile(`(?m)^\s*import(?:\s+type)?\s+.*?from\s+['"]([^'"]+)['"]`)
	reImportBare = regexp.MustCompile(`(?m)^\s*import\s+['"]([^'"]+)['"]`)
	reRequire    = regexp.MustCompile(`(?m)require\(\s*['"]([^'"]+)['"]\s*\)`)
	reDynamic    = regexp.MustCompile(`(?m)import\(\s*['"]([^'"]+)['"]\s*\)`)
	reExportFrom = regexp.MustCompile(`(?m)^\s*export\s+.*?\sfrom\s+['"]([^'"]+)['"]`)
)

func isSource(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".ts", ".tsx":
		return true
	default:
		return false
	}
}

type Result struct {
	File    string
	Imports []string
	Err     error
}

type Unresolved struct {
	File string
	Spec string
	Err  error
}

func isRelativeImport(spec string) bool {
	return strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")
}

// Extracts import specifiers from file contents.
// content is a string that contains code
// it returns a slice of unique module names that were imported or required
func ParseImports(content string) []string {
	seen := map[string]struct{}{}

	// helper function where ms is a slice of regex submatches from FindAllStringSubmatch
	// each match is one match
	// match[0] is a full match, like import x from "react"
	// match[1] is the module itself, "react"
	// it will trim whitespace and if non empty insert the module name into seen
	add := func(matches [][]string) {
		for _, match := range matches {
			if len(match) > 1 {
				module := strings.TrimSpace(match[1])
				if module != "" {
					seen[module] = struct{}{}
				}
			}
		}
	}

	add(reImportFrom.FindAllStringSubmatch(content, -1))
	add(reImportBare.FindAllStringSubmatch(content, -1))
	add(reRequire.FindAllStringSubmatch(content, -1))
	add(reDynamic.FindAllStringSubmatch(content, -1))
	add(reExportFrom.FindAllStringSubmatch(content, -1))

	// Normalize, ignore style/assets and node builtins
	out := make([]string, 0, len(seen))
	for module := range seen {
		l := strings.ToLower(module)
		if strings.HasSuffix(l, ".css") || strings.HasSuffix(l, ".scss") || strings.HasSuffix(l, ".less") || strings.HasSuffix(l, ".yml") {
			continue
		}
		out = append(out, module)
	}
	return out
}

// Very simple implementation of module resolution. This 100% gets re-written
// fromFile is the file that contains the import
// spec is the import string from that file
// This returns the resolved file path
//
// This currently only hands relative paths
func Resolve(fromFile, spec string) (string, error) {
	// Leave non-relative imports (packages, absolute aliases) as is for now.
	if !(strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/")) {
		return "pkg:" + spec, nil
	}

	// Build a candidate path.
	// Find the directory of fromFile, join it with spec to get the target path and remove the relative path
	// string safely
	base := filepath.Dir(fromFile)
	candidate := filepath.Clean(filepath.Join(base, spec))

	// exact path as given, if it already has an extension
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, nil
	}

	// Try common extensions
	extensions := []string{".ts", ".tsx"}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		// try index/barrel files
		for _, extension := range extensions {
			try := filepath.Join(candidate, "index"+extension)
			if info2, err2 := os.Stat(try); err2 == nil && !info2.IsDir() {
				return try, nil
			}
		}
	}

	// Try appending extensions when candidate has no extension
	if filepath.Ext(candidate) == "" {
		for _, extension := range extensions {
			try := candidate + extension
			if info, err := os.Stat(try); err == nil && !info.IsDir() {
				return try, nil
			}
		}
	}

	// build an error showing what we tried
	var attempts []string
	// record directory barrel attempts
	if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
		for _, extension := range extensions {
			attempts = append(attempts, filepath.Join(candidate, "index"+extension))
		}
	}

	// record extension attempts if no extension
	if filepath.Ext(candidate) == "" {
		attempts = append(attempts, candidate)
	}

	if len(attempts) == 0 {
		attempts = []string{candidate}
	}

	return "", fmt.Errorf("could not resolve %q from %q; tried: %v", spec, fromFile, attempts)
}

// Walks through a source tree, parses imports, and builds a directed dependency graph concurrently.
// ctx lets us cancel the work early
// root is the root directory of the project.
// returns a pointer to graph.Graph containing dependency edges between files.
func BuildGraph(ctx context.Context, root string) (*graph.Graph, error) {
	g := graph.New()
	// Channel of file paths (producer-consumer pattern here)
	fileChannel := make(chan string, 1024)
	// A channel of results from worker go routines
	resultChannel := make(chan Result, 1024)

	// Producer to walk files concurrently
	go func() {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if d.IsDir() {
				// skip junk
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" || name == "build" {
					return filepath.SkipDir
				}
				return nil
			}
			if isSource(path) {
				fileChannel <- path
			}
			return nil
		})
		close(fileChannel)
	}()

	// workers
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for path := range fileChannel {
				data, err := os.ReadFile(path)
				if err != nil {
					resultChannel <- Result{File: path, Err: err}
					continue
				}
				imports := ParseImports(string(data))
				resultChannel <- Result{File: path, Imports: imports, Err: nil}
			}
		}()
	}

	// Closer
	go func() {
		wg.Wait()
		close(resultChannel)
	}()

	unresolved := make([]Unresolved, 0, 64)

	// Consume results
	for {
		select {
		case <-ctx.Done():
			return g, ctx.Err()

		case r, ok := <-resultChannel:
			if !ok {
				// finished all results
				if len(unresolved) > 0 {
					var b strings.Builder
					b.WriteString("some imports could not be resolved:\n")
					for _, u := range unresolved {
						fmt.Fprintf(&b, "- %s: import %q: %v\n", u.File, u.Spec, u.Err)
					}
					// choose: fail hard, or return g, nil (partial graph)
					return g, fmt.Errorf(b.String())
				}
				return g, nil
			}

			if r.Err != nil {
				// read/parse error for this file—skip (or collect separately)
				continue
			}

			g.Touch(r.File)

			for _, spec := range r.Imports {
				to, err := Resolve(r.File, spec)
				if err != nil {
					// Only treat as unresolved if it was a relative spec;
					// externals are now dropped/kept without error.
					if isRelativeImport(spec) {
						unresolved = append(unresolved, Unresolved{File: r.File, Spec: spec, Err: err})
					}
					continue
				}
				if to == "" {
					// dropped external (Option A)
					continue
				}

				// If it’s relative, sanity-check the resolved path exists (defensive)
				if isRelativeImport(spec) {
					info, statErr := os.Stat(to)
					if statErr != nil || info.IsDir() {
						reason := statErr
						if statErr == nil && info.IsDir() {
							reason = fmt.Errorf("resolved to directory without index: %s", to)
						}
						unresolved = append(unresolved, Unresolved{File: r.File, Spec: spec, Err: reason})
						continue
					}
				}

				g.AddEdge(r.File, to)
			}
		}
	}
}

func FirstLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}

	defer f.Close()

	sc := bufio.NewScanner(f)

	lines := []string{}
	for i := 0; i < n && sc.Scan(); i++ {
		lines = append(lines, sc.Text())
	}

	return strings.Join(lines, "\n"), sc.Err()
}

// BuildGraphFromEntries: multi-root, entry-driven traversal.
// This walks only the reachable dependency closure starting from the given entries,
// which is better for MPAs (Rails + many React roots) and faster on large repos.
func BuildGraphFromEntries(ctx context.Context, root string, entries []Entry) (*graph.Graph, error) {
	g := graph.New()

	// queue carries files to visit; we close it automatically when "inflight" hits zero.
	queue := make(chan string, 4096)

	// visited ensures we process each file at most once (prevents cycles & duplicate work).
	visited := make(map[string]struct{})
	var mu sync.Mutex

	// inflight tracks how many items have been enqueued but not fully processed
	// (safe across goroutines). When it reaches zero, we close the queue.
	var inflight int64

	// enqueue adds a path to the queue exactly once and bumps the inflight counter.
	enqueue := func(p string) {
		mu.Lock()
		if _, seen := visited[p]; !seen {
			visited[p] = struct{}{}
			atomic.AddInt64(&inflight, 1)
			queue <- p
		}
		mu.Unlock()
	}

	// Seed the traversal with the provided entries (resolve relative to root).
	for _, e := range entries {
		start := e.Path
		if !filepath.IsAbs(start) {
			start = filepath.Clean(filepath.Join(root, start))
		}
		enqueue(start)
	}

	// Spin up workers to process the queue concurrently.
	workers := runtime.NumCPU()
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case path, ok := <-queue:
					if !ok {
						// queue closed: nothing more to do for this worker
						return
					}

					// Read file and parse imports. Errors are non-fatal: we just skip the file.
					data, err := os.ReadFile(path)
					if err == nil {
						g.Touch(path)
						for _, spec := range ParseImports(string(data)) {
							to, rerr := Resolve(path, spec)
							if rerr == nil {
								// Record the edge no matter if it's internal or external (pkg:...).
								g.AddEdge(path, to)

								// Only enqueue reachable local files (skip pkg: externals)
								if isRelativeImport(spec) {
									if info, statErr := os.Stat(to); statErr == nil && !info.IsDir() {
										enqueue(to)
									}
								}
							}
						}
					}

					// Mark this item as fully processed. If this was the last in-flight item,
					// close the queue so all workers can drain and exit.
					if atomic.AddInt64(&inflight, -1) == 0 {
						close(queue)
					}
				}
			}
		}()
	}

	// Wait for all workers to finish or context cancellation.
	wg.Wait()
	return g, ctx.Err()
}
