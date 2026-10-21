package bitbucket

import (
	"context"
	"net/http"

	forge "github.com/git-pkgs/forge"
)

type bitbucketWebhookService struct{}

func (f *bitbucketForge) Webhooks() forge.WebhookService {
	return &bitbucketWebhookService{}
}

func (s *bitbucketWebhookService) List(_ context.Context, _, _ string, _ forge.ListWebhookOpts) ([]forge.Webhook, error) {
	return nil, forge.ErrNotSupported
}

func (s *bitbucketWebhookService) Get(_ context.Context, _, _ string, _ int64) (*forge.Webhook, error) {
	return nil, forge.ErrNotSupported
}

func (s *bitbucketWebhookService) Create(_ context.Context, _, _ string, _ forge.CreateWebhookOpts) (*forge.Webhook, error) {
	return nil, forge.ErrNotSupported
}

func (s *bitbucketWebhookService) Delete(_ context.Context, _, _ string, _ int64) error {
	return forge.ErrNotSupported
}

func (s *bitbucketWebhookService) ParseEvent(_ *http.Request, _ string) (*forge.WebhookEvent, error) {
	return nil, forge.ErrNotSupported
}
