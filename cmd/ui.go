package cmd

import (
	"context"
	"embed"
	"encoding/json"
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

	"github.com/philjestin/philtographer/internal/graph"
	"github.com/philjestin/philtographer/internal/scan"
	"github.com/philjestin/philtographer/internal/scan/providers"
	"github.com/philjestin/philtographer/internal/tsgraph"
)

//go:embed ui_static/*
var uiFS embed.FS

var (
	uiAddr         string
	uiGraph        string
	uiEvents       string
	uiMode         = "scan" // scan | components
	uiIncludeDeps  bool
	uiAffectedOnly bool
	uiConfig       scan.Config

	// background watcher state
	uiWatchMu     sync.Mutex
	uiWatchCancel context.CancelFunc
)

// uiCmd serves a small static UI to visualize a graph.json via D3.
var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Serve a local UI for viewing graph.json as a force-directed graph",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Default to tmp graph/events if not provided, and ensure valid JSON files exist
		if strings.TrimSpace(uiGraph) == "" {
			uiGraph = filepath.Join("tmp", "graph.json")
		}
		if strings.TrimSpace(uiEvents) == "" {
			uiEvents = filepath.Join("tmp", "events.json")
		}
		_ = os.MkdirAll(filepath.Dir(uiGraph), 0o755)
		_ = os.MkdirAll(filepath.Dir(uiEvents), 0o755)
		ensureJSONFile(uiGraph, map[string]interface{}{"nodes": []string{}, "edges": []map[string]string{}})
		ensureJSONFile(uiEvents, map[string]interface{}{"ts": 0, "changed": []string{}, "impacted": []string{}})

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

		// API endpoints for config management and triggering scans
		mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"config":       uiConfig,
					"mode":         uiMode,
					"includeDeps":  uiIncludeDeps,
					"affectedOnly": uiAffectedOnly,
					"graphPath":    uiGraph,
					"eventsPath":   uiEvents,
				})
			case http.MethodPost:
				var in struct {
					Config       scan.Config `json:"config"`
					Mode         string      `json:"mode"`
					IncludeDeps  bool        `json:"includeDeps"`
					AffectedOnly bool        `json:"affectedOnly"`
					GraphPath    string      `json:"graphPath"`
					EventsPath   string      `json:"eventsPath"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					http.Error(w, "bad json", http.StatusBadRequest)
					return
				}
				uiConfig = in.Config
				if uiConfig.Root == "" {
					uiConfig.Root = "."
				}
				if in.Mode == "components" {
					uiMode = "components"
				} else {
					uiMode = "scan"
				}
				uiIncludeDeps = in.IncludeDeps
				uiAffectedOnly = in.AffectedOnly
				if s := strings.TrimSpace(in.GraphPath); s != "" {
					uiGraph = s
				}
				if s := strings.TrimSpace(in.EventsPath); s != "" {
					uiEvents = s
				}
				_ = os.MkdirAll(filepath.Dir(uiGraph), 0o755)
				_ = os.MkdirAll(filepath.Dir(uiEvents), 0o755)
				ensureJSONFile(uiGraph, map[string]interface{}{"nodes": []string{}, "edges": []map[string]string{}})
				ensureJSONFile(uiEvents, map[string]interface{}{"ts": time.Now().UnixMilli(), "changed": []string{}, "impacted": []string{}})
				// begin watching new paths and notify clients to refresh
				startFileWatcher(uiGraph, uiEvents)
				wsBroadcast()
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})

		mux.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			root := uiConfig.Root
			if root == "" {
				root = "."
			}
			var outGraph interface{}
			var err error
			if uiMode == "components" {
				var provs []providers.Provider
				for _, spec := range uiConfig.Entries {
					switch spec.Type {
					case "rootsTs":
						provs = append(provs, providers.RootsTsProvider{File: spec.File, NameFrom: spec.NameFrom})
					case "explicit":
						provs = append(provs, providers.ExplicitProvider{Name: spec.Name, Path: spec.Path})
					}
				}
				seen := map[string]bool{}
				var entryPaths []string
				for _, p := range provs {
					es, derr := p.Discover(ctx, root)
					if derr != nil {
						err = derr
						break
					}
					for _, e := range es {
						if !seen[e.Path] {
							seen[e.Path] = true
							entryPaths = append(entryPaths, e.Path)
						}
					}
				}
				if err == nil {
					if len(entryPaths) == 0 {
						entryPaths = []string{root}
					}
					if gg, berr := tsgraph.BuildComponentGraphFromEntries(ctx, root, entryPaths); berr == nil {
						outGraph = gg
					} else {
						err = berr
					}
				}
			} else {
				outGraph, err = scan.BuildGraph(ctx, root)
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if werr := writeJSONFile(uiGraph, outGraph); werr != nil {
				http.Error(w, werr.Error(), http.StatusInternalServerError)
				return
			}
			// minimal event update to trigger UI refresh
			evt := struct {
				Timestamp int64    `json:"ts"`
				Changed   []string `json:"changed"`
				Impacted  []string `json:"impacted"`
			}{Timestamp: time.Now().UnixMilli(), Changed: nil, Impacted: nil}
			_ = writeJSONFile(uiEvents, evt)
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		// Start/Stop background watching without CLI
		mux.HandleFunc("/api/watch", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var in struct {
				Action string `json:"action"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			switch strings.ToLower(strings.TrimSpace(in.Action)) {
			case "start":
				startUIWatcher()
				writeJSON(w, http.StatusOK, map[string]string{"status": "watching"})
			case "stop":
				stopUIWatcher()
				writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
			default:
				http.Error(w, "action must be start or stop", http.StatusBadRequest)
			}
		})
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

// ensureJSONFile creates the file with JSON content if missing or invalid.
func ensureJSONFile(p string, v interface{}) {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		f, err := os.Open(p)
		if err == nil {
			defer f.Close()
			var tmp interface{}
			if json.NewDecoder(f).Decode(&tmp) == nil {
				return
			}
		}
	}
	_ = writeJSONFile(p, v)
}

// writeJSON encodes v as JSON with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// startUIWatcher launches a background fsnotify-based watcher that mirrors the CLI watch behavior.
func startUIWatcher() {
	uiWatchMu.Lock()
	defer uiWatchMu.Unlock()
	if uiWatchCancel != nil {
		uiWatchCancel()
		uiWatchCancel = nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	uiWatchCancel = cancel
	go runUIWatcher(ctx)
}

func stopUIWatcher() {
	uiWatchMu.Lock()
	defer uiWatchMu.Unlock()
	if uiWatchCancel != nil {
		uiWatchCancel()
		uiWatchCancel = nil
	}
}

func runUIWatcher(ctx context.Context) {
	root := uiConfig.Root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("ui watch:", err)
		return
	}
	defer watcher.Close()
	if _, err := addRecursiveWithCount(watcher, root); err != nil {
		log.Println("ui watch add:", err)
	}
	for _, d := range scan.NewResolver(root).WatchDirs() {
		_ = watcher.Add(d)
	}
	var mu sync.Mutex
	pending := map[string]struct{}{}
	var timer *time.Timer
	flush := func() {
		mu.Lock()
		files := make([]string, 0, len(pending))
		for f := range pending {
			files = append(files, f)
		}
		pending = map[string]struct{}{}
		mu.Unlock()
		if len(files) == 0 {
			return
		}
		build := func(ctx context.Context, changed []string) (*graph.Graph, []string, error) {
			switch uiMode {
			case "components":
				var provs []providers.Provider
				for _, spec := range uiConfig.Entries {
					switch spec.Type {
					case "rootsTs":
						provs = append(provs, providers.RootsTsProvider{File: spec.File, NameFrom: spec.NameFrom})
					case "explicit":
						provs = append(provs, providers.ExplicitProvider{Name: spec.Name, Path: spec.Path})
					}
				}
				seen := map[string]bool{}
				var entryPaths []string
				for _, p := range provs {
					es, err := p.Discover(ctx, root)
					if err != nil {
						return nil, nil, err
					}
					for _, e := range es {
						if !seen[e.Path] {
							seen[e.Path] = true
							entryPaths = append(entryPaths, e.Path)
						}
					}
				}
				if len(entryPaths) == 0 {
					entryPaths = []string{root}
				}
				g, err := tsgraph.BuildComponentGraphFromEntries(ctx, root, entryPaths)
				if err != nil {
					return g, nil, err
				}
				return g, impactedForChanges(root, g, changed), nil
			default:
				g, err := scan.BuildGraph(ctx, root)
				if err != nil {
					return g, nil, err
				}
				return g, impactedForChanges(root, g, changed), nil
			}
		}
		_ = doRebuild(root, build, uiGraph, uiEvents, files, uiAffectedOnly)
	}
	schedule := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(120*time.Millisecond, flush)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-watcher.Events:
			if !isWatchedFile(ev.Name) {
				continue
			}
			p := ev.Name
			if !filepath.IsAbs(p) {
				if a, err := filepath.Abs(p); err == nil {
					p = a
				}
			}
			mu.Lock()
			pending[filepath.Clean(p)] = struct{}{}
			mu.Unlock()
			schedule()
		case err := <-watcher.Errors:
			log.Println("ui watch error:", err)
		}
	}
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
