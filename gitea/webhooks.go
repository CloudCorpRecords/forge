package gitea

import (
	"context"
	forge "github.com/git-pkgs/forge"

	"code.gitea.io/sdk/gitea"
)

type giteaWebhookService struct {
	client *gitea.Client
}

func (f *giteaForge) Webhooks() forge.WebhookService {
	return &giteaWebhookService{client: f.client}
}

func convertGiteaHook(h *gitea.Hook) forge.Webhook {
	return forge.Webhook{
		ID:          h.ID,
		URL:         h.Config["url"],
		Events:      h.Events,
		Active:      h.Active,
		ContentType: h.Config["content_type"],
		CreatedAt:   h.Created,
		UpdatedAt:   h.Updated,
	}
}

func (s *giteaWebhookService) List(ctx context.Context, owner, repo string, opts forge.ListWebhookOpts) ([]forge.Webhook, error) {
	perPage := pageSize(opts.PerPage)
	page := opts.Page
	if page <= 0 {
		page = 1
	}

	var all []forge.Webhook
	for {
		hooks, resp, err := s.client.ListRepoHooks(owner, repo, gitea.ListHooksOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: perPage},
		})
		if err != nil {
			return nil, wrapErr("list webhooks", resp, err)
		}
		for _, h := range hooks {
			all = append(all, convertGiteaHook(h))
		}
		if lastPage(resp, len(hooks), perPage) || (opts.Limit > 0 && len(all) >= opts.Limit) {
			break
		}
		page++
	}

	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}

	return all, nil
}

func (s *giteaWebhookService) Get(ctx context.Context, owner, repo string, id int64) (*forge.Webhook, error) {
	h, resp, err := s.client.GetRepoHook(owner, repo, id)
	if err != nil {
		return nil, wrapErr("get webhook", resp, err)
	}

	wh := convertGiteaHook(h)
	return &wh, nil
}

func (s *giteaWebhookService) Create(ctx context.Context, owner, repo string, opts forge.CreateWebhookOpts) (*forge.Webhook, error) {
	contentType := opts.ContentType
	if contentType == "" {
		contentType = "json"
	}
	events := opts.Events
	if len(events) == 0 {
		events = []string{"push"}
	}

	config := map[string]string{
		"url":          opts.URL,
		"content_type": contentType,
	}
	if opts.Secret != "" {
		config["secret"] = opts.Secret
	}

	h, resp, err := s.client.CreateRepoHook(owner, repo, gitea.CreateHookOption{
		Type:   gitea.HookTypeGitea,
		Config: config,
		Events: events,
		Active: true,
	})
	if err != nil {
		return nil, wrapErr("create webhook", resp, err)
	}

	wh := convertGiteaHook(h)
	return &wh, nil
}

func (s *giteaWebhookService) Delete(ctx context.Context, owner, repo string, id int64) error {
	resp, err := s.client.DeleteRepoHook(owner, repo, id)
	if err != nil {
		return wrapErr("delete webhook", resp, err)
	}
	return nil
}
