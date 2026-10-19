package bitbucket

import (
	"context"

	forge "github.com/git-pkgs/forge"
)

type bitbucketCheckRunService struct{}

func (f *bitbucketForge) CheckRuns() forge.CheckRunService {
	return &bitbucketCheckRunService{}
}

func (s *bitbucketCheckRunService) List(_ context.Context, _, _, _ string) ([]forge.CheckRun, error) {
	return nil, forge.ErrNotSupported
}
