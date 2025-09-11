package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

var (
	isoGraph string
)

// isolatedCmd prints nodes with degree 0 (no inbound or outbound edges) from a graph JSON file.
var isolatedCmd = &cobra.Command{
	Use:   "isolated",
	Short: "Print nodes with no inbound or outbound edges from a graph.json file",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isoGraph == "" {
			return fmt.Errorf("--graph is required (path to graph.json)")
		}
		f, err := os.Open(isoGraph)
		if err != nil {
			return fmt.Errorf("open --graph: %w", err)
		}
		defer f.Close()

		var g struct {
			Nodes []string `json:"nodes"`
			Edges []struct {
				From string `json:"From"`
				To   string `json:"To"`
			} `json:"edges"`
		}
		if err := json.NewDecoder(f).Decode(&g); err != nil {
			return fmt.Errorf("decode graph: %w", err)
		}

		outdeg := make(map[string]int, len(g.Nodes))
		indeg := make(map[string]int, len(g.Nodes))
		for _, n := range g.Nodes {
			outdeg[n] = 0
			indeg[n] = 0
		}
		for _, e := range g.Edges {
			outdeg[e.From] = outdeg[e.From] + 1
			indeg[e.To] = indeg[e.To] + 1
		}

		var isolated []string
		for _, n := range g.Nodes {
			if outdeg[n] == 0 && indeg[n] == 0 {
				isolated = append(isolated, n)
			}
		}
		sort.Strings(isolated)
		for _, n := range isolated {
			fmt.Println(n)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(isolatedCmd)
	isolatedCmd.Flags().StringVar(&isoGraph, "graph", "", "path to graph.json to analyze")
}
