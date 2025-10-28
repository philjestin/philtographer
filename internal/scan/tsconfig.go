package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// tsConfigCompiler models the subset of tsconfig we care about.
type tsConfigCompiler struct {
	CompilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// Resolver loads tsconfig paths and resolves module specifiers to files.
type Resolver struct {
	root    string
	baseDir string // root/baseUrl
	paths   map[string][]string
}

// NewResolver loads tsconfig.base.json or tsconfig.json under root.
func NewResolver(root string) *Resolver {
	r := &Resolver{root: root}
	// Determine tsconfig path preference
	try := []string{"tsconfig.base.json", "tsconfig.json"}
	var cfg tsConfigCompiler
	for _, name := range try {
		p := filepath.Join(root, name)
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &cfg)
			break
		}
	}
	r.paths = cfg.CompilerOptions.Paths
	if cfg.CompilerOptions.BaseURL != "" {
		// baseUrl is relative to tsconfig file directory
		r.baseDir = filepath.Clean(filepath.Join(root, cfg.CompilerOptions.BaseURL))
	} else {
		r.baseDir = root
	}
	return r
}

// Resolve resolves relative, absolute, alias, and bare specs.
// Returns "pkg:<name>" for bare specs with no alias.
func (r *Resolver) Resolve(fromFile, spec string) (string, error) {
	// Relative or absolute handled via file probing
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/") {
		return resolveFile(fromFile, spec)
	}
	// Try alias patterns from tsconfig paths
	if to, ok := r.resolveAlias(spec); ok {
		return to, nil
	}
	// Try baseUrl fallback (treat bare spec as relative to baseDir)
	if to := r.resolveFromBase(spec); to != "" {
		return to, nil
	}
	// Bare package: leave tagged
	return "pkg:" + spec, nil
}

// WatchDirs returns directories that should be watched to capture edits that
// affect alias/baseUrl resolution (baseDir and tsconfig paths targets).
func (r *Resolver) WatchDirs() []string {
	uniq := map[string]struct{}{}
	dirs := []string{}
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if _, ok := uniq[p]; ok {
			return
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			uniq[p] = struct{}{}
			dirs = append(dirs, p)
		}
	}
	// Always include root and baseDir if they exist
	add(r.root)
	add(r.baseDir)
	// Include directories derived from paths mappings
	for _, globs := range r.paths {
		for _, g := range globs {
			s := g
			if idx := strings.Index(s, "*"); idx >= 0 {
				s = s[:idx]
			}
			abs := filepath.Clean(filepath.Join(r.baseDir, s))
			add(abs)
			add(filepath.Dir(abs))
		}
	}
	return dirs
}

// resolveAlias tries to match compilerOptions.paths patterns.
func (r *Resolver) resolveAlias(spec string) (string, bool) {
	if len(r.paths) == 0 {
		return "", false
	}
	// Direct match first
	if globs, ok := r.paths[spec]; ok {
		for _, g := range globs {
			if to := r.probeAliasTarget(g); to != "" {
				return to, true
			}
		}
	}
	// Wildcard match: pattern like @pkg/*
	for pat, globs := range r.paths {
		if !strings.Contains(pat, "*") {
			continue
		}
		head := strings.Split(pat, "*")[0]
		if !strings.HasPrefix(spec, head) {
			continue
		}
		tail := strings.TrimPrefix(spec, head)
		for _, g := range globs {
			repl := strings.ReplaceAll(g, "*", tail)
			if to := r.probeAliasTarget(repl); to != "" {
				return to, true
			}
		}
	}
	return "", false
}

// resolveFromBase attempts to resolve a bare spec relative to baseDir.
func (r *Resolver) resolveFromBase(spec string) string {
	if spec == "" {
		return ""
	}
	cand := filepath.Clean(filepath.Join(r.baseDir, spec))
	if to, err := resolveFile(filepath.Join(r.baseDir, "index.ts"), relFromBase(r.baseDir, cand)); err == nil && to != "" {
		return to
	}
	return ""
}

// probeAliasTarget resolves a tsconfig path mapping value to a concrete file.
func (r *Resolver) probeAliasTarget(target string) string {
	// Targets are relative to baseDir
	cand := filepath.Clean(filepath.Join(r.baseDir, target))
	// Reuse file probing from resolveFile logic by faking a fromFile in baseDir
	if to, err := resolveFile(filepath.Join(r.baseDir, "index.ts"), relFromBase(r.baseDir, cand)); err == nil && to != "" {
		return to
	}
	return ""
}

func relFromBase(base, abs string) string {
	if filepath.IsAbs(abs) {
		if rel, err := filepath.Rel(filepath.Dir(base), abs); err == nil && !strings.HasPrefix(rel, ".") {
			return "./" + rel
		}
		return abs
	}
	return abs
}

// --- helpers shared with legacy Resolve ---

func resolveFile(fromFile, spec string) (string, error) {
	base := filepath.Dir(fromFile)
	candidate := filepath.Clean(filepath.Join(base, spec))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate, nil
	}
	extensions := []string{".ts", ".tsx", ".js", ".jsx"}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		for _, extension := range extensions {
			try := filepath.Join(candidate, "index"+extension)
			if info2, err2 := os.Stat(try); err2 == nil && !info2.IsDir() {
				return try, nil
			}
		}
	}
	if filepath.Ext(candidate) == "" {
		for _, extension := range extensions {
			try := candidate + extension
			if info, err := os.Stat(try); err == nil && !info.IsDir() {
				return try, nil
			}
		}
	}
	// Build attempts list for error context
	return "", os.ErrNotExist
}
