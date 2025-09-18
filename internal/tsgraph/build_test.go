package tsgraph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildComponentGraph_EdgesAndJsxResolution(t *testing.T) {
	dir := t.TempDir()
	// a.tsx renders <B/>, imports from ./b (no extension)
	a := write(t, filepath.Join(dir, "a.tsx"), `
        import { B } from './b'
        export function A(){ return <B/> }
    `)
	// b.jsx is resolved via extension and declares B
	write(t, filepath.Join(dir, "b.jsx"), `
        export function B(){ return null }
    `)

	g, err := BuildComponentGraphFromEntries(context.Background(), dir, []string{a})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nodes := g.Nodes()
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %v", nodes)
	}
}

func TestBuildComponentGraph_NamespaceAndDefaultImports(t *testing.T) {
    dir := t.TempDir()
    // a.tsx uses NS.Widget and Default
    a := write(t, filepath.Join(dir, "a.tsx"), `
        import * as NS from './lib/widgets'
        import Default from './lib/default'
        export function A(){ return <><NS.Widget/><Default/></> }
    `)
    // lib/widgets.tsx exports Widget
    write(t, filepath.Join(dir, "lib", "widgets.tsx"), `
        export function Widget(){ return null }
    `)
    // lib/default.ts exports default component
    write(t, filepath.Join(dir, "lib", "default.tsx"), `
        export default function Default(){ return null }
    `)
    g, err := BuildComponentGraphFromEntries(context.Background(), dir, []string{a})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(g.Nodes()) < 3 {
        t.Fatalf("expected >=3 nodes, got %v", g.Nodes())
    }
}

func TestBuildComponentGraph_Cycle(t *testing.T) {
    dir := t.TempDir()
    a := write(t, filepath.Join(dir, "A.tsx"), `
        import { B } from './B'
        export function A(){ return <B/> }
    `)
    write(t, filepath.Join(dir, "B.tsx"), `
        import { A } from './A'
        export function B(){ return <A/> }
    `)
    g, err := BuildComponentGraphFromEntries(context.Background(), dir, []string{a})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    // Should not deadlock; nodes contain both files
    ns := g.Nodes()
    if len(ns) < 2 {
        t.Fatalf("expected 2 nodes, got %v", ns)
    }
}
