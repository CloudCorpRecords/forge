//go:build !tinygo

package gitlab

import (
	"context"

	forge "github.com/git-pkgs/forge"
)

type gitLabCheckRunService struct{}

func (f *gitLabForge) CheckRuns() forge.CheckRunService {
	return &gitLabCheckRunService{}
}

func (s *gitLabCheckRunService) List(_ context.Context, _, _, _ string) ([]forge.CheckRun, error) {
	return nil, forge.ErrNotSupported
}
