package gitea

import (
	"context"

	forge "github.com/git-pkgs/forge"
)

type giteaCheckRunService struct{}

func (f *giteaForge) CheckRuns() forge.CheckRunService {
	return &giteaCheckRunService{}
}

func (s *giteaCheckRunService) List(_ context.Context, _, _, _ string) ([]forge.CheckRun, error) {
	return nil, forge.ErrNotSupported
}
