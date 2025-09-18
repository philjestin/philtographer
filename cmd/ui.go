package cmd

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed ui_static/*
var uiFS embed.FS

var (
	uiAddr   string
	uiGraph  string
	uiEvents string
)

// uiCmd serves a small static UI to visualize a graph.json via D3.
var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Serve a local UI for viewing graph.json as a force-directed graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		if uiGraph == "" {
			return fmt.Errorf("--graph is required (path to graph.json)")
		}
		// Validate graph file exists and is valid JSON once on startup for faster feedback.
		f, err := os.Open(uiGraph)
		if err != nil {
			return fmt.Errorf("open --graph: %w", err)
		}
		defer f.Close()
		var tmp interface{}
		if err := json.NewDecoder(f).Decode(&tmp); err != nil {
			return fmt.Errorf("invalid graph JSON: %w", err)
		}

		mux := http.NewServeMux()
		// Serve embedded static files
		fs := http.FS(uiFS)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			p := r.URL.Path
			if p == "/" {
				p = "/ui_static/index.html"
			} else if p == "/app.js" || p == "/styles.css" {
				p = "/ui_static" + p
			} else if p == "/favicon.ico" {
				w.WriteHeader(http.StatusNoContent)
				return
			} else if p == "/graph.json" {
				serveGraphJSON(w, uiGraph)
				return
			} else if p == "/events.json" {
				serveGraphJSON(w, uiEvents)
				return
			} else {
				// try to serve any other embedded asset under ui_static
				p = "/ui_static" + p
			}

			// Trim the leading slash for embedded FS access
			p = strings.TrimPrefix(p, "/")

			f, err := fs.Open(p)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()

			// Set content-type from extension when possible
			if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
				w.Header().Set("Content-Type", ct)
			}
			// Prevent aggressive caching of embedded assets during development
			w.Header().Set("Cache-Control", "no-store")

			if _, err := io.Copy(w, f); err != nil {
				// TODO: optional logging
			}
		})

		if uiEvents == "" {
			// default to sibling of graph
			uiEvents = strings.TrimSuffix(uiGraph, filepath.Ext(uiGraph)) + "-events.json"
		}
		log.Printf("UI listening on http://localhost%s (graph: %s, events: %s)\n", uiAddr, uiGraph, uiEvents)
		return http.ListenAndServe(uiAddr, mux)
	},
}

// serveGraphJSON streams the file from disk for each request to allow live reload after rescans.
func serveGraphJSON(w http.ResponseWriter, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	io.Copy(w, f)
}

func init() {
	rootCmd.AddCommand(uiCmd)
	uiCmd.Flags().StringVar(&uiAddr, "addr", ":8080", "address to listen on (e.g. :8080)")
	uiCmd.Flags().StringVar(&uiGraph, "graph", "", "path to graph.json to serve at /graph.json")
	uiCmd.Flags().StringVar(&uiEvents, "events", "", "path to events.json to serve at /events.json")
}
