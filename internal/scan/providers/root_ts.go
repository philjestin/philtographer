package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/philjestin/philtographer/internal/scan"
)

// RootsTsProvider parses a file like frontend/roots.ts and extracts entries from:
//
//	Name: { moduleFactory: () => import(/* webpackChunkName: "Name" */ "./components/foo/root") }
//
// We name entries by object key by default, optionally by webpackChunkName.
type RootsTsProvider struct {
	File     string // path to roots.ts (relative to workspace or absolute)
	NameFrom string // "objectKey" (default) or "webpackChunkName"
}

var (
	// Captures: 1=ObjectKey, 2=import path, 3=optional chunkname
	// We keep it permissive for comments/whitespace.
	reRootMember = regexp.MustCompile(`(?s)([A-Za-z0-9_]+)\s*:\s*{[^}]*?moduleFactory\s*:\s*\(\s*\)\s*=>\s*import\(\s*(?:/\*\s*webpackChunkName:\s*"(.*?)"\s*\*/\s*)?['"]([^'"]+)['"]\s*\)`)
)

func (r RootsTsProvider) Discover(ctx context.Context, workspaceRoot string) ([]scan.Entry, error) {
	// Resolve path relative to workspace
	path := r.File
	if !filepath.IsAbs(path) {
		path = filepath.Clean(filepath.Join(workspaceRoot, r.File))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read roots.ts: %w", err)
	}

	matches := reRootMember.FindAllStringSubmatch(string(b), -1)
	entries := make([]scan.Entry, 0, len(matches))

	baseDir := filepath.Dir(path)
	for _, m := range matches {
		objectKey := m[1]
		chunkName := m[2]
		importRel := m[3]

		// Choose label
		name := objectKey
		if r.NameFrom == "webpackChunkName" && chunkName != "" {
			name = chunkName
		}

		// Compute absolute path for the entry file
		entryPath := importRel
		if !filepath.IsAbs(entryPath) {
			entryPath = filepath.Clean(filepath.Join(baseDir, importRel))
		}

		entries = append(entries, scan.Entry{
			Name: name,
			Path: entryPath,
		})
	}

	return entries, nil
}
