package tsgraph

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/philjestin/philtographer/internal/graph"
)

// BuildComponentGraphFromEntries walks reachable TSX files from entries and adds edges ComponentFile -> ImportedComponentFile when JSX uses imported identifiers.
func BuildComponentGraphFromEntries(ctx context.Context, root string, entries []string) (*graph.Graph, error) {
	return BuildComponentGraphFromEntriesProgress(ctx, root, entries, nil)
}

// BuildComponentGraphFromEntriesProgress is the same as BuildComponentGraphFromEntries but reports progress snapshots.
// progress may be nil. When non-nil, it receives snapshots of (visitedFiles, edgesAdded, filesEnqueued).
func BuildComponentGraphFromEntriesProgress(
	ctx context.Context,
	root string,
	entries []string,
	progress func(visited, edges, queued int),
) (*graph.Graph, error) {
	g := graph.New()
	var gmu sync.Mutex

	type job struct{ path string }
	jobs := make(chan job, 2048)

	var visitedCount atomic.Int64
	var edgesCount atomic.Int64
	var enqueuedCount atomic.Int64
	var inflight atomic.Int64

	visited := map[string]struct{}{}
	var mu sync.Mutex
	enqueue := func(p string) {
		mu.Lock()
		defer mu.Unlock()
		if _, ok := visited[p]; ok {
			return
		}
		visited[p] = struct{}{}
		enqueuedCount.Add(1)
		inflight.Add(1)
		jobs <- job{path: p}
	}

	for _, e := range entries {
		p := e
		if !filepath.IsAbs(p) {
			p = filepath.Clean(filepath.Join(root, p))
		}
		enqueue(p)
	}

	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					// drain: decrement inflight for this job and potentially close
					if inflight.Add(-1) == 0 {
						close(jobs)
					}
					return
				default:
				}
				data, err := os.ReadFile(j.path)
				if err == nil {
					if fi, perr := ParseTSX(j.path, data); perr == nil {
						gmu.Lock()
						g.Touch(j.path)
						gmu.Unlock()
						visitedCount.Add(1)
						for _, ident := range fi.JSXIdentifiers {
							if to := ResolveImportedComponent(j.path, fi.ImportMap, ident); to != "" {
								gmu.Lock()
								g.AddEdge(j.path, to)
								gmu.Unlock()
								edgesCount.Add(1)
								enqueue(to)
							}
						}
					}
				}
				if progress != nil {
					v := int(visitedCount.Load())
					e := int(edgesCount.Load())
					q := int(enqueuedCount.Load())
					progress(v, e, q)
				}
				// mark this job done; if this was the last, close the queue
				if inflight.Add(-1) == 0 {
					close(jobs)
				}
			}
		}()
	}

	wg.Wait()
	return g, ctx.Err()
}
