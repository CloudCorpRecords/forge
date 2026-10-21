package gitea

import (
	"context"
	"encoding/json"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"net/http/httptest"
	"testing"

	"code.gitea.io/sdk/gitea"
)

func TestGiteaListWebhooks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("GET /api/v1/repos/octocat/hello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]*gitea.Hook{
			{
				ID:     1,
				Type:   "gitea",
				Config: map[string]string{"url": "https://example.com/hook", "content_type": "json"},
				Events: []string{
					"wiki", "pull_request_sync", "issue_label", "push", "pull_request",
					"pull_request_comment", "issues", "issue_assign", "issue_comment",
				},
				Active: true,
			},
			{
				ID:     2,
				Type:   "gitea",
				Config: map[string]string{"url": "https://example.com/other", "content_type": "form"},
				Events: []string{"release"},
				Active: false,
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	hooks, err := f.Webhooks().List(context.Background(), "octocat", "hello-world", forge.ListWebhookOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	assertEqual(t, "hooks[0].URL", "https://example.com/hook", hooks[0].URL)
	assertEqual(t, "hooks[0].ContentType", "json", hooks[0].ContentType)
	assertSliceEqual(t, "hooks[0].Events", []string{"push", "issues", "issue_comment", "pull_request", "gollum"}, hooks[0].Events)
	assertSliceEqual(t, "hooks[1].Events", []string{"release"}, hooks[1].Events)
	assertEqualBool(t, "hooks[0].Active", true, hooks[0].Active)
	assertEqual(t, "hooks[1].ContentType", "form", hooks[1].ContentType)
	assertEqualBool(t, "hooks[1].Active", false, hooks[1].Active)
}

func TestGiteaCreateWebhook(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("POST /api/v1/repos/octocat/hello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		var body gitea.CreateHookOption
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		assertEqual(t, "type", "gitea", body.Type)
		assertEqualBool(t, "active", true, body.Active)
		assertSliceEqual(t, "events", []string{"push"}, body.Events)
		assertEqual(t, "config.url", "https://example.com/hook", body.Config["url"])
		assertEqual(t, "config.content_type", "json", body.Config["content_type"])
		assertEqual(t, "config.secret", "s3cret", body.Config["secret"])

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(gitea.Hook{
			ID:     7,
			Type:   "gitea",
			Config: map[string]string{"url": "https://example.com/hook", "content_type": "json"},
			Events: []string{"push"},
			Active: true,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	hook, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
		URL:    "https://example.com/hook",
		Secret: "s3cret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook.ID != 7 {
		t.Errorf("ID: want 7, got %d", hook.ID)
	}
	assertEqual(t, "URL", "https://example.com/hook", hook.URL)
}

func TestGiteaGetWebhookNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("GET /api/v1/repos/octocat/hello-world/hooks/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	_, err := f.Webhooks().Get(context.Background(), "octocat", "hello-world", 999)
	if err != forge.ErrNotFound {
		t.Fatalf("expected forge.ErrNotFound, got %v", err)
	}
}

func TestGiteaDeleteWebhook(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("DELETE /api/v1/repos/octocat/hello-world/hooks/7", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	if err := f.Webhooks().Delete(context.Background(), "octocat", "hello-world", 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGiteaCreateWebhookTranslatesEvents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("POST /api/v1/repos/octocat/hello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		var body gitea.CreateHookOption
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		assertSliceEqual(t, "events", []string{
			"wiki",
			"issues_only", "issue_assign", "issue_label", "issue_milestone",
			"pull_request_only", "pull_request_assign", "pull_request_label",
			"pull_request_milestone", "pull_request_sync", "pull_request_review_request",
		}, body.Events)

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(gitea.Hook{
			ID:     8,
			Type:   "gitea",
			Config: map[string]string{"url": "https://example.com/hook", "content_type": "json"},
			Events: []string{
				"pull_request_review_request", "wiki", "issue_milestone", "pull_request",
				"issue_label", "pull_request_assign", "issues", "pull_request_sync",
				"issue_assign", "pull_request_label", "pull_request_milestone",
			},
			Active: true,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	hook, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
		URL:    "https://example.com/hook",
		Events: []string{"gollum", "issues", "pull_request"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSliceEqual(t, "Events", []string{"issues", "pull_request", "gollum"}, hook.Events)
}

func TestGiteaCreateWebhookUnsupportedEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("POST /api/v1/repos/octocat/hello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request for unsupported event")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	for _, event := range []string{"wiki", "watch", "issues_only"} {
		_, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
			URL:    "https://example.com/hook",
			Events: []string{"push", event},
		})
		if err == nil {
			t.Errorf("event %q: expected error, got nil", event)
		}
	}
}

func TestGiteaCreateWebhookRejectsVersionDependentEvents(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	mux.HandleFunc("POST /api/v1/repos/octocat/hello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request for version-dependent event")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	for _, event := range []string{"status", "workflow_run", "workflow_job"} {
		t.Run(event, func(t *testing.T) {
			if _, err := toGiteaEvents([]string{event}); err == nil {
				t.Errorf("toGiteaEvents(%q): expected error, got nil", event)
			}
			_, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
				URL:    "https://example.com/hook",
				Events: []string{"push", event},
			})
			if err == nil {
				t.Errorf("Create with %q: expected error, got nil", event)
			}
		})
	}
}

func TestGiteaWebhookEventMapping(t *testing.T) {
	tests := []struct {
		name   string
		create []string
		read   []string
	}{
		{"push", []string{"push"}, []string{"push"}},
		{"create", []string{"create"}, []string{"create"}},
		{"delete", []string{"delete"}, []string{"delete"}},
		{"fork", []string{"fork"}, []string{"fork"}},
		{"issues", []string{"issues_only", "issue_assign", "issue_label", "issue_milestone"}, []string{"issues", "issue_assign", "issue_label", "issue_milestone"}},
		{"issue_comment", []string{"issue_comment", "pull_request_comment"}, []string{"issue_comment", "pull_request_comment"}},
		{"pull_request", []string{"pull_request_only", "pull_request_assign", "pull_request_label", "pull_request_milestone", "pull_request_sync", "pull_request_review_request"}, []string{"pull_request", "pull_request_assign", "pull_request_label", "pull_request_milestone", "pull_request_sync", "pull_request_review_request"}},
		{"pull_request_review", []string{"pull_request_review"}, []string{"pull_request_review_approved", "pull_request_review_rejected", "pull_request_review_comment"}},
		{"release", []string{"release"}, []string{"release"}},
		{"repository", []string{"repository"}, []string{"repository"}},
		{"gollum", []string{"wiki"}, []string{"wiki"}},
		{"package", []string{"package"}, []string{"package"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toGiteaEvents([]string{tt.name})
			if err != nil {
				t.Fatalf("toGiteaEvents: %v", err)
			}
			assertSliceEqual(t, "create", tt.create, got)
			assertSliceEqual(t, "read", []string{tt.name}, fromGiteaEvents(tt.read))
		})
	}
}

func TestGiteaWebhookEventsDefaultToPush(t *testing.T) {
	got, err := toGiteaEvents(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSliceEqual(t, "events", []string{"push"}, got)
}

func TestGiteaWebhookEventsKeepUnknownOnRead(t *testing.T) {
	got := fromGiteaEvents([]string{"schedule", "push", "schedule"})
	assertSliceEqual(t, "events", []string{"push", "schedule"}, got)
}
