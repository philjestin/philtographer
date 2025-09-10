package providers

import (
	"context"
	"path/filepath"

	"github.com/philjestin/philtographer/internal/scan"
)

type ExplicitProvider struct {
	Name string
	Path string
}

func (e ExplicitProvider) Discover(ctx context.Context, workspaceRoot string) ([]scan.Entry, error) {
	p := e.Path
	if !filepath.IsAbs(p) {
		p = filepath.Clean(filepath.Join(workspaceRoot, p))
	}
	return []scan.Entry{{Name: e.Name, Path: p}}, nil
}
