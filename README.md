# philtographer

`philtographer` is a CLI for building dependency graphs of TypeScript/React codebases, with first‑class support for large monorepos and multi‑root apps. It can:

- Impact analysis: find reverse dependents of a file ("what breaks if I change X?")
- Visualize graphs in a local D3 UI
- Power watch workflows that recompute and display impacted sets live

---

## Quickstart

```bash
# From project root
# 1) Generate a graph (to tmp/graph.json)
go run ./cmd/rg scan --out tmp/graph.json

# 2) Serve the UI at http://localhost:8080
go run ./cmd/rg ui --graph tmp/graph.json --events tmp/events.json
```

Live updates while editing source (recommended two terminals):

```bash
# Terminal A: watch and rebuild on changes
go run ./cmd/rg watch --graph tmp/graph.json --events tmp/events.json

# Terminal B: serve the UI
go run ./cmd/rg ui --graph tmp/graph.json --events tmp/events.json
```

Using a config file with an absolute root (example):

```jsonc
// philtographer.config.json
{
  "root": "/abs/path/to/your/src",
  "out": "graph.json",
  "entries": [
    { "type": "rootsTs", "file": "/abs/path/to/roots.ts", "nameFrom": "objectKey" },
    { "type": "explicit", "name": "frontend", "path": "/abs/path/to/entry-or-dir" }
  ]
}
```

---

## Installation

- Requires Go ≥ 1.25 (see `go.mod`).
- Option A (build binary):

```bash
git clone https://github.com/philjestin/philtographer.git
cd philtographer
make build # produces ./bin/philtographer
```

- Option B (no build): use the Go toolchain directly (examples in this README use `go run ./cmd/rg …`).

---

## Configuration

`philtographer` reads configuration from:
- Flags (highest precedence)
- Environment variables (prefix `PHILTOGRAPHER_`)
- Config file (`--config`, or auto-detected `./philtographer.config.{json,yaml,toml}`)

Config schema (JSON shown):

```jsonc
{
  "root": ".",                 // project root to scan (absolute or relative)
  "out": "graph.json",         // default output path when a command writes to a file
  "entries": [                  // optional: providers to discover entry points
    { "type": "rootsTs", "file": "./path/to/roots.ts", "nameFrom": "objectKey|webpackChunkName" },
    { "type": "explicit", "name": "frontend", "path": "./path/to/entry-or-dir" }
  ],
  "watchDirs": ["src", "apps"] // optional: limit watch to these dirs (abs or relative to root)
}
```

Environment variables:
- `PHILTOGRAPHER_ROOT` – set project root (same as `--root`)
- `PHILTOGRAPHER_WORKERS` – cap concurrent file parsing workers (default: CPU count)
- `PHILTOGRAPHER_MAX_WATCH_DIRS` – cap directories added to fsnotify (fallbacks kick in automatically)

---

## Commands

Global flags:
- `--config <file>` – config path (auto-detects `./philtographer.config.*` if omitted)
- `--root <dir>` – project root (overrides config)
- `--out <file>` – output file for commands that write graphs

### scan
Walk the entire source tree under `--root` and produce a full dependency graph.

```bash
# to file
go run ./cmd/rg scan --root ./src --out graph.json

# to stdout
go run ./cmd/rg scan --root ./src
```

Notes:
- Resolves `.ts/.tsx/.js/.jsx` (including `index.*` resolutions)
- Bare imports become nodes tagged `pkg:<name>`

### entries
Discover entry points from configured providers and build a graph of their reachable closure.

```bash
go run ./cmd/rg entries --config ./philtographer.config.json --out graph.json
```

Providers:
- `rootsTs`: parse a `roots.ts` object containing dynamic `import(...)` entries
  - `file`: path to `roots.ts`
  - `nameFrom`: `objectKey` (default) or `webpackChunkName`
- `explicit`: specify `name` + `path` directly

### components
Build a React component usage graph by walking from entries and following actually used TSX imports.

```bash
# from config entries
go run ./cmd/rg components --config ./philtographer.config.json --out component-graph.json

# or from a single root directory or file
go run ./cmd/rg components --root ./frontend/app --out component-graph.json
```

### isolated
List nodes without inbound or outbound edges in a previously generated graph.

```bash
go run ./cmd/rg isolated --graph ./graph.json
```

### watch
Watch the workspace, rebuild on changes, and compute the impacted set. Writes a graph plus an events file.

```bash
# Full dependency graph watch
go run ./cmd/rg watch \
  --mode scan \
  --root ./your/workspace \
  --graph ./tmp/graph.json \
  --events ./tmp/events.json

# Component graph watch (uses entries; falls back to root/index.*)
go run ./cmd/rg watch \
  --mode components \
  --config ./philtographer.config.json \
  --graph ./tmp/component-graph.json \
  --events ./tmp/component-events.json

# Only write affected subgraph after each change
go run ./cmd/rg watch --mode scan --root ./your/workspace \
  --graph ./tmp/graph.json --events ./tmp/events.json --affected-only
```

Flags:
- `--mode`: `scan` | `components`
- `--graph`: output graph JSON path (required)
- `--events`: output events JSON path (default: sibling of `--graph` with `-events.json` suffix)
- `--affected-only`: write only the subgraph of changed + impacted nodes
- `--include-deps`: include forward dependencies from importer seeds in the impacted set

Event model:
- `graph.json`: either the full graph or the affected-only subgraph (nodes + edges)
- `events.json`: `{ ts, changed[], impacted[] }` for the latest rebuild

Large repos:
- The watcher prefers selective watching based on tsconfig `baseUrl/paths` when possible
- If system watch limits are hit, it falls back to a hierarchical strategy automatically
- You can constrain or expand watching via config `watchDirs` or env `PHILTOGRAPHER_MAX_WATCH_DIRS`

### ui
Serve a local UI to visualize a `graph.json` and reflect live changes.

```bash
# Default port :8080
go run ./cmd/rg ui --graph tmp/graph.json --events tmp/events.json
# Open http://localhost:8080
```

HTTP API (optional, to drive scans from the UI):

```bash
# Set config and output paths
curl -s -X POST http://localhost:8080/api/config \
  -H 'Content-Type: application/json' \
  -d '{
    "config": { "root": "/abs/path/to/src" },
    "mode": "scan",
    "graphPath": "tmp/graph.json",
    "eventsPath": "tmp/events.json",
    "affectedOnly": false,
    "includeDeps": false
  }'

# Trigger a scan using current UI config
curl -s -X POST http://localhost:8080/api/scan
```

---

## Graph format

Both `scan` and `entries` output JSON like:

```json
{
  "nodes": ["src/components/Button.tsx", "pkg:react"],
  "edges": [{ "From": "src/components/Button.tsx", "To": "pkg:react" }]
}
```

- `nodes`: files and external packages
- `edges`: directed `from → to` meaning “from imports to”

---

## Troubleshooting

- Too many open files / watch limits
  - The tool auto‑switches to a hierarchical mode when limits are hit
  - Set `PHILTOGRAPHER_MAX_WATCH_DIRS` (e.g., `2000`) to cap directories
  - Use `watchDirs` in config to constrain watch scope
- Slow scans
  - Reduce workers via `PHILTOGRAPHER_WORKERS=4`
  - Limit `root` to the relevant part of your repo

---

## Development

- `make build` – build the binary into `./bin/philtographer`
- `make run ARGS="scan --root ./src"` – run with args
- `make test` – run tests

Useful tests:

```bash
go test ./internal/scan
go test ./internal/tsgraph
go test ./...
```

