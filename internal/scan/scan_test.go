package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseImports_FiltersAssetsAndGlobs(t *testing.T) {
	src := strings.Join([]string{
		`import x from "./module"`,
		`import type y from "../types"`,
		`import "./styles.css"`,
		`import img from "../*.jpg"`,
		`const a = require("./a.png")`,
		`export * from "./b.svg"`,
	}, "\n")
	got := ParseImports(src)
	for _, m := range got {
		if strings.Contains(m, "*.jpg") || strings.HasSuffix(m, ".css") || strings.HasSuffix(m, ".png") || strings.HasSuffix(m, ".svg") {
			t.Fatalf("unexpected asset/glob import kept: %s", m)
		}
	}
}

func TestResolve_JsAndJsxAndIndex(t *testing.T) {
	dir := t.TempDir()
	// Create structure:
	//   comp/index.jsx
	//   util.js
	compDir := filepath.Join(dir, "comp")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(compDir, "index.jsx"), []byte("export default 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "util.js"), []byte("module.exports=1"), 0o644); err != nil {
		t.Fatal(err)
	}

	from := filepath.Join(dir, "main.tsx")

	// Resolve directory to index.jsx
	to, err := Resolve(from, "./comp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(to, filepath.Join("comp", "index.jsx")) {
		t.Fatalf("expected index.jsx, got %s", to)
	}

	// Resolve file without extension to .js
	to2, err := Resolve(from, "./util")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(to2, "util.js") {
		t.Fatalf("expected util.js, got %s", to2)
	}
}

func TestBuildGraphFromEntries_TransitiveAndExternals(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ts")
	b := filepath.Join(dir, "b.ts")
	c := filepath.Join(dir, "c.ts")
	if err := os.WriteFile(a, []byte("import './b'; import React from 'react';"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("export const x = 1; export { default as C } from './c'"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("export default 42"), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := BuildGraphFromEntries(context.Background(), dir, []Entry{{Name: "a", Path: a}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nodes := g.Nodes()
	// Expect at least a,b,c plus pkg:react
	expect := map[string]bool{"pkg:react": true, a: true, b: true, c: true}
	for k := range expect {
		found := false
		for _, n := range nodes {
			if n == k {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected node %s in graph, got %v", k, nodes)
		}
	}
}
