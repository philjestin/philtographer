package scan

import (
	"bytes"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	ts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// parseImportsAST extracts module specifiers using tree-sitter (TS/TSX), covering
// import statements, export ... from, require(), and dynamic import().
// On parse failure, it returns nil to allow callers to fall back to regex.
func parseImportsAST(path string, content []byte) []string {
	parser := sitter.NewParser()
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".ts" {
		parser.SetLanguage(ts.GetLanguage())
	} else {
		parser.SetLanguage(tsx.GetLanguage())
	}
	tree := parser.Parse(nil, content)
	if tree == nil {
		return nil
	}
	root := tree.RootNode()
	out := map[string]struct{}{}

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if !n.IsNamed() {
			return
		}
		switch n.Type() {
		case "import_statement", "export_statement":
			// import ... from "module";  export ... from "module";
			for i := 0; i < int(n.NamedChildCount()); i++ {
				c := n.NamedChild(i)
				if c.Type() == "string" {
					spec := strings.Trim(string(content[c.StartByte():c.EndByte()]), "'\"")
					if spec != "" {
						out[spec] = struct{}{}
					}
				}
			}
		case "call_expression":
			// require("module") or import("module")
			if n.NamedChildCount() >= 2 {
				callee := n.NamedChild(0)
				args := n.NamedChild(1)
				if callee != nil && (callee.Type() == "identifier" && (nodeText(content, callee) == "require" || nodeText(content, callee) == "import")) {
					// first string literal argument
					for i := 0; i < int(args.NamedChildCount()); i++ {
						a := args.NamedChild(i)
						if a.Type() == "string" {
							spec := strings.Trim(string(content[a.StartByte():a.EndByte()]), "'\"")
							if spec != "" {
								out[spec] = struct{}{}
							}
							break
						}
					}
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root)
	// normalize and filter like ParseImports
	specs := make([]string, 0, len(out))
	for s := range out {
		specs = append(specs, s)
	}
	filtered := make([]string, 0, len(specs))
	for _, module := range specs {
		l := strings.ToLower(module)
		if strings.Contains(module, "*") ||
			strings.HasSuffix(l, ".css") || strings.HasSuffix(l, ".scss") || strings.HasSuffix(l, ".less") || strings.HasSuffix(l, ".yml") ||
			strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".jpeg") || strings.HasSuffix(l, ".png") || strings.HasSuffix(l, ".gif") || strings.HasSuffix(l, ".svg") ||
			strings.HasSuffix(l, ".mp3") || strings.HasSuffix(l, ".mp4") {
			continue
		}
		filtered = append(filtered, module)
	}
	return filtered
}

func nodeText(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(bytes.TrimSpace(src[n.StartByte():n.EndByte()]))
}
