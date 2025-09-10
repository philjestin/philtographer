package scan

type Entry struct {
	Name string // optional label (root name)
	Path string // absolute or workspace-relative path to the entry file
}
