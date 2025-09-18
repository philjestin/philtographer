# philtographer

`philtographer` is a CLI tool for **building dependency graphs** of large TypeScript/React codebases, with a focus on **monorepos** and **multi-root / multi-page applications** (e.g. Rails apps with many React roots).

It scans your source tree, discovers entry points (roots), and produces a machine-readable graph (`graph.json`) that you can use for:

- Impact analysis (“what files depend on this file?”)  
- Visualizations (e.g., render in Graphviz/D3)  
- Build optimization (identify change impact sets)

---

## Installation

Clone the repo and build:

```bash
git clone https://github.com/philjestin/philtographer.git
cd philtographer
make build
```

This produces a binary at `./bin/philtographer`.

---

## Usage

```bash
./bin/philtographer [command] [flags]
```

Global flags:

- `--config <file>`: Path to config file (`philtographer.config.json|yaml|toml`).  
  Default: looks for `./philtographer.config.*`.
- `--root <dir>`: Root of repo to scan. Default: current directory (`.`).  
- `--out <file>`: File to write graph JSON to. Default: stdout.

---

## Commands

### `scan`

Walk the entire source tree under `--root` and build a full dependency graph.

```bash
./bin/philtographer scan --root ./src --out graph.json
```

- Resolves .ts/.tsx plus .js/.jsx, including index.* candidates
- External/bare imports are tagged as "pkg:<name>"
- Asset and glob imports (e.g., *.png, *.svg, ../*.jpg) are ignored
- Unresolved relatives no longer fail the scan; a partial graph is returned

---

### `entries`

- Resolves .ts/.tsx plus .js/.jsx, including index.* candidates
- External/bare imports are tagged as "pkg:<name>"
- Asset and glob imports (e.g., *.png, *.svg, ../*.jpg) are ignored
- Unresolved relatives no longer fail the scan; a partial graph is returned

Discover **entry points** (roots) from config providers (e.g. `roots.ts`) and build a graph of only their **reachable closure**.  

This is the recommended mode for **multi-page apps** or monorepos where you don’t want the entire tree.

```bash
./bin/philtographer entries --config ./philtographer.config.json --out graph.json
```

Example config (`philtographer.config.json`):

```jsonc
{
  "root": ".",
  "out": "graph.json",
  "entries": [
    {
      "type": "rootsTs",
      "file": "./next/clients/employer/src/roots.ts",
      "nameFrom": "objectKey"
    },
    {
      "type": "explicit",
      "name": "AdminDashboard",
      "path": "./src/pages/admin/index.tsx"
    }
  ]
}
```

Supported entry providers:
- **rootsTs**: Parse a `roots.ts` file with dynamic `moduleFactory: () => import(...)` entries.  
  - `file`: path to roots.ts.  
  - `nameFrom`: `"objectKey"` (default) or `"webpackChunkName"`.  
- **explicit**: Provide explicit `name` + `path`.

Flags:
- `--verbose`: Show debug logs (config used, entries discovered).  
- `--print-entries`: List discovered entries and exit (no graph build).

---

### `components`

Build a React component-to-component usage graph by walking from discovered entries and following TSX imports that are actually used in JSX.

```bash
# From entries in config (rootsTs/explicit)
./bin/philtographer components --config ./philtographer.config.json --out component-graph.json

# Or point at a single root/dir (uses index.* if a directory)
./bin/philtographer components --root ./frontend/app --out component-graph.json
```

- Uses the same entry providers as `entries` (`rootsTs`, `explicit`).
- If no entries are configured, `--root` may point to an entry file or a directory with `index.tsx|ts|jsx|js`.
- Progress is printed to stderr; output is JSON written to `--out` or stdout.

---

### `isolated`

Print nodes that have no inbound or outbound edges in a previously generated graph JSON.

```bash
./bin/philtographer isolated --graph ./graph.json
```

- `--graph`: path to the graph JSON file (required)
- Outputs one file path per line (sorted).

---

### `watch`

Watch the workspace for changes, rebuild the graph, compute the impacted set, and stream updates to the UI.

```bash
# Full-module graph watch
./bin/philtographer watch \
  --mode scan \
  --root ./your/workspace \
  --graph ./tmp/graph.json \
  --events ./tmp/events.json

# Component graph watch (uses entries or --root/index.* fallback)
./bin/philtographer watch \
  --mode components \
  --config ./philtographer.config.json \
  --graph ./tmp/component-graph.json \
  --events ./tmp/component-events.json

# Only write the affected subgraph after a change
./bin/philtographer watch --mode scan --root ./your/workspace \
  --graph ./tmp/graph.json --events ./tmp/events.json --affected-only
```

Flags:
- `--mode`: `scan` (full dependency graph) or `components` (TSX component graph)
- `--graph`: output graph JSON path
- `--events`: output events JSON path (changed + impacted)
- `--affected-only`: write a subgraph after each change (smaller + faster)

When `--affected-only` is used, `graph.json` includes both the union subgraph and per-changed roots:

```json
{
  "nodes": ["…union nodes…"],
  "edges": [{"From":"…","To":"…"}],
  "graphs": [
    {"root":"/abs/path/to/changed.tsx","nodes":[…],"edges":[…]},
    {"root":"/abs/path/to/another-change.tsx","nodes":[…],"edges":[…]}
  ]
}
```

### `ui`

Serve a local UI to visualize a `graph.json` (union or per-changed subgraphs) and stream updates live.

```bash
./bin/philtographer ui --graph ./tmp/graph.json --events ./tmp/events.json --addr :8080
```

- Live updates: the UI opens a WebSocket to the server and hot‑reloads when `graph.json` or `events.json` changes.
- Open `http://localhost:8080`.

---

### Graph output format

Both `scan` and `entries` produce JSON like:

```json
{
  "nodes": [
    "src/components/Button.tsx",
    "src/components/Icon.tsx",
    "pkg:react"
  ],
  "edges": [
    { "From": "src/components/Button.tsx", "To": "src/components/Icon.tsx" },
    { "From": "src/components/Button.tsx", "To": "pkg:react" }
  ]
}
```

- **nodes**: All files + external packages.  
- **edges**: Directed edges `from → to` meaning “from imports to”.

This format is easy to consume in visualization tools or for further analysis.

---

## Example workflows

### Discover roots and build graph
```bash
./bin/philtographer entries --config ./philtographer.config.json --print-entries --verbose
./bin/philtographer entries --config ./philtographer.config.json
cat graph.json | jq .
```

### Full repo scan (all `.ts/.tsx` under repo root)
```bash
./bin/philtographer scan --root ./frontend --out full-graph.json
```

### Analyze impact
After generating a graph, you can use the Go API directly (`graph.Impacted("path/to/file.tsx")`) to find all dependents.

---

## Roadmap / Ideas

- Add providers for `webpack.stats.json`, `vite.config.js`, or glob patterns.  
- Tag nodes with their originating root (so you can split graphs per page).  
- Visualize directly in browser with a devtools panel.

---

## Development

- `make build` – build the binary.  
- `make run ARGS="scan --root ./src"` – run with args.  
- `make test` – run tests.  
- `make clean` – remove build artifacts.

Some additional testing commands

```
go test ./internal/scan
go test ./internal/tsgraph
go test ./...
```

---

## UI (Force-Directed Graph Viewer)

Serve a lightweight UI to visualize a `graph.json` with live updates (see `watch` above for recommended flow).

Key features:
- Large autosuggest search with keyboard navigation.
- Two‑row header; graph controls on second row.
- Right sidebar with:
  - Views: Union + pills for each changed root (when `graphs` is present)
  - Changed: clickable chips focus nodes
  - Impacted: clickable chips focus nodes
- Resizable sidebar (drag the vertical handle); canvas resizes in lock‑step.
- Pan/zoom (drag, wheel, pinch), Force/Tree layouts, label toggle, depth/direction focus.

Data refresh:
- On each change, `events.json` contains `{ ts, changed[], impacted[] }` and the UI updates the sidebar and focuses the set.
- When `graphs` exists in `graph.json`, use the “Views” pills to switch between Union and per‑changed subgraphs.

