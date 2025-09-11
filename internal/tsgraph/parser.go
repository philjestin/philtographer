package tsgraph

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	ts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// FileInfo contains extracted symbols for a TS/TSX file.
type FileInfo struct {
	Path           string
	Components     []string          // component identifiers declared in this file
	ImportMap      map[string]string // local name -> resolved module (raw string)
	JSXIdentifiers []string          // JSX element names encountered (top-level identifiers)
}

// ParseTSFile extracts components, imports, and JSX tag identifiers using tree-sitter TypeScript/TSX.
func ParseTSFile(path string, content []byte) (FileInfo, error) {
	parser := sitter.NewParser()
	// Choose language by extension, fallback to TSX
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".ts" {
		parser.SetLanguage(ts.GetLanguage())
	} else {
		parser.SetLanguage(tsx.GetLanguage())
	}
	root := parser.Parse(nil, content)
	if root == nil {
		return FileInfo{}, fmt.Errorf("parse failed: %s", path)
	}

	info := FileInfo{Path: path, ImportMap: map[string]string{}}

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if !n.IsNamed() {
			return
		}
		switch n.Type() {
		case "import_statement":
			mod := findChildContent(content, n, "string")
			if mod != "" {
				mod = strings.Trim(mod, "'\"")
			}
			clause := findChild(n, "import_clause")
			if clause != nil {
				if id := findChild(clause, "identifier"); id != nil {
					info.ImportMap[nodeText(content, id)] = mod
				}
				if nb := findChild(clause, "named_imports"); nb != nil {
					for i := 0; i < int(nb.NamedChildCount()); i++ {
						el := nb.NamedChild(i)
						if el.Type() == "import_specifier" {
							name := findChildContent(content, el, "identifier")
							as := findChild(el, "as_clause")
							if as != nil {
								if aid := findChild(as, "identifier"); aid != nil {
									name = nodeText(content, aid)
								}
							}
							if name != "" {
								info.ImportMap[name] = mod
							}
						}
					}
				}
			}

		case "function_declaration":
			if id := findChild(n, "identifier"); id != nil {
				name := nodeText(content, id)
				if isComponentName(name) {
					info.Components = append(info.Components, name)
				}
			}
		case "lexical_declaration":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				vd := n.NamedChild(i)
				if vd.Type() == "variable_declarator" {
					id := findChild(vd, "identifier")
					if id == nil {
						continue
					}
					name := nodeText(content, id)
					if isComponentName(name) {
						info.Components = append(info.Components, name)
					}
				}
			}
		case "jsx_opening_element", "jsx_self_closing_element":
			if ident := jsxHeadIdent(content, n); ident != "" {
				info.JSXIdentifiers = append(info.JSXIdentifiers, ident)
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(root.RootNode())

	return info, nil
}

// Backward compatibility wrapper.
func ParseTSX(path string, content []byte) (FileInfo, error) {
	return ParseTSFile(path, content)
}

func isComponentName(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

func findChild(n *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == typ {
			return c
		}
	}
	return nil
}

func findChildContent(src []byte, n *sitter.Node, typ string) string {
	if c := findChild(n, typ); c != nil {
		return nodeText(src, c)
	}
	return ""
}

func nodeText(src []byte, n *sitter.Node) string {
	start := n.StartByte()
	end := n.EndByte()
	return string(bytes.TrimSpace(src[start:end]))
}

// jsxHeadIdent extracts the leading identifier name from a JSX element name.
// Handles <Foo>, <Foo.Bar>, and namespaced or nested identifiers by returning the head (Foo).
func jsxHeadIdent(src []byte, n *sitter.Node) string {
	// Try direct identifier child
	if id := findChild(n, "identifier"); id != nil {
		return nodeText(src, id)
	}
	// Some grammars expose a "name" child with nested structure
	if name := findChild(n, "name"); name != nil {
		if id := findChild(name, "identifier"); id != nil {
			return nodeText(src, id)
		}
		if head := firstIdentifier(src, name); head != "" {
			return head
		}
	}
	// Fallback: search first identifier inside
	return firstIdentifier(src, n)
}

func firstIdentifier(src []byte, n *sitter.Node) string {
	if n.Type() == "identifier" {
		return nodeText(src, n)
	}
	if n.Type() == "jsx_identifier" {
		return nodeText(src, n)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if name := firstIdentifier(src, n.NamedChild(i)); name != "" {
			return name
		}
	}
	return ""
}

// ResolveImportedComponent attempts to map a JSX identifier to a file path if the import is relative.
func ResolveImportedComponent(currentFile string, importMap map[string]string, ident string) string {
	mod, ok := importMap[ident]
	if !ok {
		return ""
	}
	if strings.HasPrefix(mod, "./") || strings.HasPrefix(mod, "../") || strings.HasPrefix(mod, "/") {
		cand := filepath.Clean(filepath.Join(filepath.Dir(currentFile), mod))
		exts := []string{".tsx", ".ts"}
		if filepath.Ext(cand) == "" {
			for _, e := range exts {
				if p := cand + e; fileExists(p) {
					return p
				}
			}
		} else if fileExists(cand) {
			return cand
		}
		for _, e := range exts {
			if p := filepath.Join(cand, "index"+e); fileExists(p) {
				return p
			}
		}
	}
	return ""
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
