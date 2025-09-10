package main

import "github.com/philjestin/philtographer/cmd"

// import (
// 	"context"
// 	"encoding/json"
// 	"flag"
// 	"fmt"
// 	"os"
// 	"time"

// 	"github.com/philjestin/philtographer/internal/scan"
// )

// func main() {
// 	var root string
// 	var impacted string
// 	var out string
// 	var mode string

// 	flag.StringVar(&root, "root", ".", "repo root to scan")
// 	flag.StringVar(&impacted, "impacted", "", "show files impacted by this module/file")
// 	flag.StringVar(&out, "out", "", "write graph JSON to file")
// 	flag.StringVar(&mode, "mode", "scan", "mode: scan | serve")
// 	flag.Parse()

// 	switch mode {
// 	case "scan":
// 		runScan(root, impacted, out)
// 	case "serve":
// 		fmt.Fprintln(os.Stderr, "serve mode not implemented yet; try: -mode scan")
// 	default:
// 		fmt.Fprintln(os.Stderr, "unknown mode")
// 		os.Exit(2)
// 	}
// }

// func runScan(root, impactedTarget, out string) {
// 	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
// 	defer cancel()

// 	g, err := scan.BuildGraph(ctx, root)
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "scan error: %v\n", err)
// 		os.Exit(1)
// 	}

// 	if impactedTarget != "" {
// 		imp := g.Impacted(impactedTarget)
// 		writeJSON(map[string]any{
// 			"target":   impactedTarget,
// 			"impacted": imp,
// 		})
// 		return
// 	}

// 	if out != "" {
// 		f, err := os.Create(out)
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "open out: %v\n", err)
// 			os.Exit(1)
// 		}
// 		defer f.Close()
// 		enc := json.NewEncoder(f)
// 		enc.SetIndent("", "  ")
// 		if err := enc.Encode(g); err != nil {
// 			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
// 			os.Exit(1)
// 		}
// 		fmt.Fprintf(os.Stderr, "wrote %s\n", out)
// 		return
// 	}

// 	writeJSON(g) // default to stdout
// }

// func writeJSON(v any) {
// 	enc := json.NewEncoder(os.Stdout)
// 	enc.SetIndent("", "  ")
// 	_ = enc.Encode(v)
// }

func main() {
	cmd.Execute()
}
