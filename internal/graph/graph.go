package graph

import (
	"encoding/json"
	"sort"
)

type Graph struct {
	// edges[a] is a set of imports that A depends on
	edges map[string]map[string]struct{}

	// reverse[b] is a set of files that import B.
	// we can compute lazily or via Add
	reverse map[string]map[string]struct{}
}

func New() *Graph {
	return &Graph{
		edges:   make(map[string]map[string]struct{}),
		reverse: make(map[string]map[string]struct{}),
	}
}

// This basically helps make sure that we can traverse the graph backwards
// which helps for topological sort and dependency resolution (key)
func (g *Graph) AddEdge(from, to string) {
	// if from is empty or to is empty or from is equal to to
	// return, there is nothing left to traverse
	if from == "" || to == "" || from == to {
		return
	}

	// Initialize a nested map if it doesn't exist.
	// Every time we try to add edges for a node "from", that node gets
	// an initialized adjacency set, if it doesn't it gets created.
	// we can safely add the neighbors later
	if _, ok := g.edges[from]; !ok {
		// create a new empty inner map
		// this represents the adjacency set for the node from
		g.edges[from] = make(map[string]struct{})
	}
	// Marks to as a member of the adjacency set for from
	// struct{}{} uses no extra memory and is the canonical go way to implement sets
	g.edges[from][to] = struct{}{}

	// Build a reverse adjacency list
	// if it does not exist, create it so we can safely add the members later
	if _, ok := g.reverse[to]; !ok {
		g.reverse[to] = make(map[string]struct{})
	}
	// This adds from into the set of inbound neighbors for to
	g.reverse[to][from] = struct{}{}
}

// Collects all of the unique nodes in the graph, whether they appear as a source
// or destination. Return them in a slice of strings, and ensures they are sorted.
func (g *Graph) Nodes() []string {
	// deduplicate nodes since they can appear both in edges and reverse
	seen := map[string]struct{}{}

	// Add all "from" nodes (outgoing edges)
	for node := range g.edges {
		seen[node] = struct{}{}
	}

	// Add all "to" nodes (incoming edges)
	for node := range g.reverse {
		seen[node] = struct{}{}
	}

	// Create a slice with capacity equal to the number of unique nodes
	out := make([]string, 0, len(seen))

	// Convert it into a slice
	for node := range seen {
		out = append(out, node)
	}

	sort.Strings(out)
	return out
}

// Find all nodes that directly or indirectly depend on start by walking the reverse adjacency map
// "If I change a file, which other files will be impacted."
func (g *Graph) Impacted(start string) []string {
	// Track visited nodes, prevents infinite loops in cyclic graphs and stores true when a node has been reached
	visited := map[string]bool{}
	var dfs func(n string)

	// Create a recursive closure inside of Impacted
	// it looks at all predecessors of node (all nodes that point to node)
	dfs = func(node string) {
		// ADDED: nil-guard so looking up a node with no predecessors (or unknown node)
		// does not panic with a map access on a missing key.
		preds, ok := g.reverse[node]
		if !ok {
			return
		}
		// For each predecessor predecessor, if it has not been visited
		// mark it as so and recursively search its predecessors
		for predecessor := range preds {
			if !visited[predecessor] {
				visited[predecessor] = true
				dfs(predecessor)
			}
		}
	}

	// Start from a node, start itself is not marked visited (since the check only marks predecessors).
	// so the output is only the impacted nodes, not the starting node itself.
	dfs(start)
	out := make([]string, 0, len(visited))

	for file := range visited {
		out = append(out, file)
	}

	sort.Strings(out)
	return out
}

// Whenever we do json.Marshall(g), this method will be called
// it must return json or an error
func (g *Graph) MarshalJSON() ([]byte, error) {
	// Create a tiny struct with two string fields, From and To
	// this struct will represent each edge when serialized
	type edge struct{ From, To string }

	edges := []edge{}

	// iterates over the forward adjacency list. For each from node, loop through all its to nodes
	// appends an edge into the edges slice
	// now we have every directed edge in teh graph
	for from, tos := range g.edges {
		for to := range tos {
			edges = append(edges, edge{From: from, To: to})
		}
	}

	// creates an anonymous struct with two fields.
	return json.Marshal(struct {
		Nodes []string `json:"nodes"`
		Edges []edge   `json:"edges"`
	}{
		Nodes: g.Nodes(),
		Edges: edges,
	})
}

func (g *Graph) Touch(n string) {
	if n == "" {
		return
	}

	if _, ok := g.edges[n]; !ok {
		g.edges[n] = make(map[string]struct{})
	}

	// ADDED: keep reverse map in sync so lookups like g.reverse[n]
	// are always safe (even for isolated/touched nodes).
	if _, ok := g.reverse[n]; !ok {
		g.reverse[n] = make(map[string]struct{})
	}
}
