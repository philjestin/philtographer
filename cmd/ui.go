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
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
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
			} else if p == "/ws" {
				serveWS(w, r)
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
		// Start file watcher to notify clients on changes
		startFileWatcher(uiGraph, uiEvents)
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

// --- SSE push for live updates ---
var (
	sseClientsMu sync.Mutex
	sseClients   = map[chan struct{}]struct{}{}
	wsUpgrader   = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsClientsMu  sync.Mutex
	wsClients    = map[*websocket.Conn]struct{}{}
)

func serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan struct{}, 8)
	sseClientsMu.Lock()
	sseClients[ch] = struct{}{}
	sseClientsMu.Unlock()

	// Send initial ping so client fetches right away
	io.WriteString(w, "event: ping\n data: 1\n\n")
	flusher.Flush()

	// Heartbeat to keep connections alive through proxies
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			sseClientsMu.Lock()
			delete(sseClients, ch)
			sseClientsMu.Unlock()
			return
		case <-ticker.C:
			io.WriteString(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-ch:
			io.WriteString(w, "event: update\n data: 1\n\n")
			flusher.Flush()
		}
	}
}

func serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	wsClientsMu.Lock()
	wsClients[conn] = struct{}{}
	wsClientsMu.Unlock()
	// simple reader to consume and ignore messages; close on error
	go func() {
		defer func() { wsClientsMu.Lock(); delete(wsClients, conn); wsClientsMu.Unlock(); conn.Close() }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func wsBroadcast() {
	wsClientsMu.Lock()
	for c := range wsClients {
		_ = c.WriteControl(websocket.PingMessage, []byte("1"), time.Now().Add(2*time.Second))
		_ = c.WriteMessage(websocket.TextMessage, []byte("update"))
	}
	wsClientsMu.Unlock()
}

func startFileWatcher(graphPath, eventsPath string) {
	go func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Println("sse watcher:", err)
			return
		}
		defer watcher.Close()
		add := func(p string) {
			if p != "" {
				_ = watcher.Add(filepath.Dir(p))
			}
		}
		add(graphPath)
		add(eventsPath)
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only notify for the target files
				if (graphPath != "" && (ev.Name == graphPath)) || (eventsPath != "" && (ev.Name == eventsPath)) {
					sseClientsMu.Lock()
					for ch := range sseClients {
						select {
						case ch <- struct{}{}:
						default:
						}
					}
					sseClientsMu.Unlock()
					wsBroadcast()
				}
			case err := <-watcher.Errors:
				log.Println("sse watcher error:", err)
			}
		}
	}()
}

func init() {
	rootCmd.AddCommand(uiCmd)
	uiCmd.Flags().StringVar(&uiAddr, "addr", ":8080", "address to listen on (e.g. :8080)")
	uiCmd.Flags().StringVar(&uiGraph, "graph", "", "path to graph.json to serve at /graph.json")
	uiCmd.Flags().StringVar(&uiEvents, "events", "", "path to events.json to serve at /events.json")
}
