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
	// Try nearest tsconfig.json/tsconfig.base.json up from fromFile directory
	if to, ok := r.resolveWithNearest(fromFile, spec); ok {
		return to, nil
	}
	// Try baseUrl fallback (treat bare spec as relative to baseDir)
	if to := r.resolveFromBase(spec); to != "" {
		return to, nil
	}
	// Bare package: leave tagged
	return "pkg:" + spec, nil
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

// resolveFromBase tries to resolve a bare spec under baseUrl directory.
func (r *Resolver) resolveFromBase(spec string) string {
	if r.baseDir == "" {
		return ""
	}
	// Join baseDir with spec and probe like resolveFile would for a relative path
	cand := filepath.Clean(filepath.Join(r.baseDir, spec))
	// Exact file
	if info, err := os.Stat(cand); err == nil && !info.IsDir() {
		return cand
	}
	// If directory, try index.*
	extensions := []string{".ts", ".tsx", ".js", ".jsx"}
	if info, err := os.Stat(cand); err == nil && info.IsDir() {
		for _, extension := range extensions {
			try := filepath.Join(cand, "index"+extension)
			if info2, err2 := os.Stat(try); err2 == nil && !info2.IsDir() {
				return try
			}
		}
	}
	// If no extension, try appending
	if filepath.Ext(cand) == "" {
		for _, extension := range extensions {
			try := cand + extension
			if info, err := os.Stat(try); err == nil && !info.IsDir() {
				return try
			}
		}
	}
	return ""
}

// resolveWithNearest tries to load the nearest tsconfig.* above fromFile and resolve using its paths/baseUrl.
func (r *Resolver) resolveWithNearest(fromFile, spec string) (string, bool) {
	dir := filepath.Dir(fromFile)
	stop := r.root
	for {
		baseDir, paths, ok := loadCompilerAt(dir)
		if ok {
			// direct match
			if to := resolveWithPaths(baseDir, paths, spec); to != "" {
				return to, true
			}
			// baseUrl fallback
			if baseDir != "" {
				if to := resolveFromBaseDir(baseDir, spec); to != "" {
					return to, true
				}
			}
		}
		if dir == stop || dir == filepath.Dir(dir) {
			break
		}
		dir = filepath.Dir(dir)
	}
	return "", false
}

// loadCompilerAt reads tsconfig.base.json or tsconfig.json in dir.
func loadCompilerAt(dir string) (string, map[string][]string, bool) {
	try := []string{"tsconfig.base.json", "tsconfig.json"}
	var cfg tsConfigCompiler
	for _, name := range try {
		p := filepath.Join(dir, name)
		if b, err := os.ReadFile(p); err == nil {
			if json.Unmarshal(b, &cfg) == nil {
				base := dir
				if cfg.CompilerOptions.BaseURL != "" {
					base = filepath.Clean(filepath.Join(dir, cfg.CompilerOptions.BaseURL))
				}
				return base, cfg.CompilerOptions.Paths, true
			}
		}
	}
	return "", nil, false
}

// resolveWithPaths replicates alias resolution against a provided paths map and baseDir.
func resolveWithPaths(baseDir string, paths map[string][]string, spec string) string {
	if len(paths) == 0 {
		return ""
	}
	if globs, ok := paths[spec]; ok {
		for _, g := range globs {
			if to := resolveFromBaseDir(baseDir, g); to != "" {
				return to
			}
		}
	}
	for pat, globs := range paths {
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
			if to := resolveFromBaseDir(baseDir, repl); to != "" {
				return to
			}
		}
	}
	return ""
}

// resolveFromBaseDir mirrors resolveFromBase using provided baseDir.
func resolveFromBaseDir(baseDir, spec string) string {
	if baseDir == "" {
		return ""
	}
	cand := filepath.Clean(filepath.Join(baseDir, spec))
	if info, err := os.Stat(cand); err == nil && !info.IsDir() {
		return cand
	}
	extensions := []string{".ts", ".tsx", ".js", ".jsx"}
	if info, err := os.Stat(cand); err == nil && info.IsDir() {
		for _, extension := range extensions {
			try := filepath.Join(cand, "index"+extension)
			if info2, err2 := os.Stat(try); err2 == nil && !info2.IsDir() {
				return try
			}
		}
	}
	if filepath.Ext(cand) == "" {
		for _, extension := range extensions {
			try := cand + extension
			if info, err := os.Stat(try); err == nil && !info.IsDir() {
				return try
			}
		}
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

// WatchDirs returns directories implied by paths mappings to help watchers include alias targets.
func (r *Resolver) WatchDirs() []string {
	dirs := map[string]struct{}{}
	if r.baseDir != "" {
		dirs[r.baseDir] = struct{}{}
	}
	for _, globs := range r.paths {
		for _, g := range globs {
			cut := g
			if i := strings.Index(cut, "*"); i >= 0 {
				cut = cut[:i]
			}
			abs := filepath.Clean(filepath.Join(r.baseDir, cut))
			dirs[abs] = struct{}{}
		}
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	return out
}
