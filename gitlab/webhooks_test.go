//go:build !tinygo

package gitlab

import (
	"context"
	"encoding/json"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"net/http/httptest"
	"testing"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestGitLabListWebhooks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects/octocat%2Fhello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]*gitlab.ProjectHook{
			{
				ID:                    1,
				URL:                   "https://example.com/hook",
				PushEvents:            true,
				MergeRequestsEvents:   true,
				NoteEvents:            true,
				EnableSSLVerification: true,
				AlertStatus:           "executable",
			},
			{
				ID:            2,
				URL:           "https://example.com/other",
				TagPushEvents: true,
				AlertStatus:   "disabled",
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
	assertSliceEqual(t, "hooks[0].Events", []string{"push", "issue_comment", "pull_request"}, hooks[0].Events)
	assertEqualBool(t, "hooks[0].Active", true, hooks[0].Active)
	assertEqualBool(t, "hooks[0].InsecureSSL", false, hooks[0].InsecureSSL)
	assertEqual(t, "hooks[0].ContentType", "json", hooks[0].ContentType)
	assertSliceEqual(t, "hooks[1].Events", []string{"tag_push"}, hooks[1].Events)
	assertEqualBool(t, "hooks[1].Active", false, hooks[1].Active)
	assertEqualBool(t, "hooks[1].InsecureSSL", true, hooks[1].InsecureSSL)
}

func TestGitLabGetWebhookNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects/octocat%2Fhello-world/hooks/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "404 Not found"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	_, err := f.Webhooks().Get(context.Background(), "octocat", "hello-world", 999)
	if err != forge.ErrNotFound {
		t.Fatalf("expected forge.ErrNotFound, got %v", err)
	}
}

func TestGitLabCreateWebhook(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/projects/octocat%2Fhello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		want := map[string]any{
			"url":                     "https://example.com/hook",
			"token":                   "s3cret",
			"enable_ssl_verification": true,
			"push_events":             false,
			"tag_push_events":         false,
			"issues_events":           true,
			"note_events":             false,
			"merge_requests_events":   true,
			"releases_events":         false,
			"deployment_events":       false,
			"wiki_page_events":        false,
			"pipeline_events":         false,
			"job_events":              false,
		}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("%s: want %v, got %v", k, v, body[k])
			}
		}

		_ = json.NewEncoder(w).Encode(gitlab.ProjectHook{
			ID:                    5,
			URL:                   "https://example.com/hook",
			IssuesEvents:          true,
			MergeRequestsEvents:   true,
			EnableSSLVerification: true,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	hook, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
		URL:    "https://example.com/hook",
		Secret: "s3cret",
		Events: []string{"issues", "pull_request"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook.ID != 5 {
		t.Errorf("ID: want 5, got %d", hook.ID)
	}
	assertSliceEqual(t, "Events", []string{"issues", "pull_request"}, hook.Events)
}

func TestGitLabCreateWebhookDefaultsToPush(t *testing.T) {
	opts, err := gitLabHookOptions(forge.CreateWebhookOpts{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqualBool(t, "PushEvents", true, *opts.PushEvents)
	assertEqualBool(t, "IssuesEvents", false, *opts.IssuesEvents)
	if opts.Token != nil {
		t.Errorf("Token: want nil, got %q", *opts.Token)
	}
}

func TestGitLabCreateWebhookUnsupportedEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/projects/octocat%2Fhello-world/hooks", func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected request for unsupported event")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	_, err := f.Webhooks().Create(context.Background(), "octocat", "hello-world", forge.CreateWebhookOpts{
		URL:    "https://example.com/hook",
		Events: []string{"fork"},
	})
	if err == nil {
		t.Fatal("expected error for unsupported event")
	}
}

func TestGitLabDeleteWebhook(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v4/projects/octocat%2Fhello-world/hooks/5", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	if err := f.Webhooks().Delete(context.Background(), "octocat", "hello-world", 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
