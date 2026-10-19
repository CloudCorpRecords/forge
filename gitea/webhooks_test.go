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
				Events: []string{"push", "pull_request"},
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
	assertSliceEqual(t, "hooks[0].Events", []string{"push", "pull_request"}, hooks[0].Events)
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
