package gitea

import (
	"context"
	"fmt"
	forge "github.com/git-pkgs/forge"

	"code.gitea.io/sdk/gitea"
)

// giteaWebhookEvents maps normalized event names onto Gitea/Forgejo hook
// events, in the order they are reported by List and Get. Gitea treats issues
// and pull_request as groups that also enable their comment and review
// events, so create sends the *_only forms plus each sub-event to keep
// GitHub's narrower meaning. Reads report the individual sub-events.
var giteaWebhookEvents = []struct {
	name   string
	create []string // events sent when creating a hook
	read   []string // events the API reports for an existing hook
}{
	{name: "push", create: []string{"push"}, read: []string{"push"}},
	{name: "create", create: []string{"create"}, read: []string{"create"}},
	{name: "delete", create: []string{"delete"}, read: []string{"delete"}},
	{name: "fork", create: []string{"fork"}, read: []string{"fork"}},
	{
		name:   "issues",
		create: []string{"issues_only", "issue_assign", "issue_label", "issue_milestone"},
		read:   []string{"issues", "issue_assign", "issue_label", "issue_milestone"},
	},
	{
		name:   "issue_comment",
		create: []string{"issue_comment", "pull_request_comment"},
		read:   []string{"issue_comment", "pull_request_comment"},
	},
	{
		name:   "pull_request",
		create: []string{"pull_request_only", "pull_request_assign", "pull_request_label", "pull_request_milestone", "pull_request_sync", "pull_request_review_request"},
		read:   []string{"pull_request", "pull_request_assign", "pull_request_label", "pull_request_milestone", "pull_request_sync", "pull_request_review_request"},
	},
	{
		name:   "pull_request_review",
		create: []string{"pull_request_review"},
		read:   []string{"pull_request_review", "pull_request_review_approved", "pull_request_review_rejected", "pull_request_review_comment"},
	},
	{name: "release", create: []string{"release"}, read: []string{"release"}},
	{name: "repository", create: []string{"repository"}, read: []string{"repository"}},
	{name: "gollum", create: []string{"wiki"}, read: []string{"wiki"}},
	{name: "package", create: []string{"package"}, read: []string{"package"}},
	{name: "status", create: []string{"status"}, read: []string{"status"}},
	{name: "workflow_run", create: []string{"workflow_run"}, read: []string{"workflow_run"}},
	{name: "workflow_job", create: []string{"workflow_job"}, read: []string{"workflow_job"}},
}

// toGiteaEvents translates normalized event names into the events sent when
// creating a hook. Names with no Gitea/Forgejo equivalent are rejected.
func toGiteaEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		events = []string{"push"}
	}
	var out []string
	for _, name := range events {
		known := false
		for _, e := range giteaWebhookEvents {
			if e.name == name {
				out = append(out, e.create...)
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("gitea: unsupported webhook event %q", name)
		}
	}
	return out, nil
}

// fromGiteaEvents translates the events reported for a hook into normalized
// names. Events with no normalized name are kept as reported.
func fromGiteaEvents(events []string) []string {
	reported := make(map[string]bool, len(events))
	for _, e := range events {
		reported[e] = true
	}

	out := []string{}
	mapped := make(map[string]bool)
	for _, e := range giteaWebhookEvents {
		found := false
		for _, r := range e.read {
			if reported[r] {
				mapped[r] = true
				found = true
			}
		}
		if found {
			out = append(out, e.name)
		}
	}
	for _, e := range events {
		if !mapped[e] {
			out = append(out, e)
			mapped[e] = true
		}
	}
	return out
}

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
		Events:      fromGiteaEvents(h.Events),
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
	events, err := toGiteaEvents(opts.Events)
	if err != nil {
		return nil, err
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
