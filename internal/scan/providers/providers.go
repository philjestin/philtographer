package providers

import (
	"context"

	"github.com/philjestin/philtographer/internal/scan"
)

// Provider is a tiny interface so we can add more discovery mechanisms later.
// Each provider returns a set of entries (roots) given the workspace root.
type Provider interface {
	Discover(ctx context.Context, workspaceRoot string) ([]scan.Entry, error)
}
