package github

import (
	"context"
	"encoding/json"
	"fmt"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v82/github"
)

func newTestGitHubWebhookService(srv *httptest.Server) *gitHubWebhookService {
	c := github.NewClient(nil)
	c, _ = c.WithEnterpriseURLs(srv.URL+"/api/v3", srv.URL+"/api/v3")
	return &gitHubWebhookService{client: c}
}

func TestGitHubWebhookList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello/hooks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[
			{"id": 1, "active": true, "events": ["push", "pull_request"], "created_at": "2024-01-15T10:00:00Z",
			 "config": {"url": "https://example.com/hook", "content_type": "json", "insecure_ssl": "0"}},
			{"id": 2, "active": false, "events": ["release"],
			 "config": {"url": "https://example.com/other", "content_type": "form", "insecure_ssl": "1"}}
		]`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestGitHubWebhookService(srv)
	hooks, err := svc.List(context.Background(), "octocat", "hello", forge.ListWebhookOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}

	assertEqual(t, "URL[0]", "https://example.com/hook", hooks[0].URL)
	assertSliceEqual(t, "Events[0]", []string{"push", "pull_request"}, hooks[0].Events)
	assertEqualBool(t, "Active[0]", true, hooks[0].Active)
	assertEqual(t, "ContentType[0]", "json", hooks[0].ContentType)
	assertEqualBool(t, "InsecureSSL[0]", false, hooks[0].InsecureSSL)
	if !hooks[0].CreatedAt.Equal(parseTime("2024-01-15T10:00:00Z")) {
		t.Errorf("CreatedAt[0]: got %v", hooks[0].CreatedAt)
	}
	assertEqualBool(t, "Active[1]", false, hooks[1].Active)
	assertEqual(t, "ContentType[1]", "form", hooks[1].ContentType)
	assertEqualBool(t, "InsecureSSL[1]", true, hooks[1].InsecureSSL)
}

func TestGitHubWebhookGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/repos/octocat/hello/hooks/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id": 1, "active": true, "events": ["push"], "config": {"url": "https://example.com/hook"}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestGitHubWebhookService(srv)
	hook, err := svc.Get(context.Background(), "octocat", "hello", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook.ID != 1 {
		t.Errorf("ID: want 1, got %d", hook.ID)
	}
	assertEqual(t, "URL", "https://example.com/hook", hook.URL)
}

func TestGitHubWebhookCreate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/repos/octocat/hello/hooks", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name   string            `json:"name"`
			Active bool              `json:"active"`
			Events []string          `json:"events"`
			Config map[string]string `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		assertEqual(t, "name", "web", body.Name)
		assertEqualBool(t, "active", true, body.Active)
		assertSliceEqual(t, "events", []string{"push", "issues"}, body.Events)
		assertEqual(t, "config.url", "https://example.com/hook", body.Config["url"])
		assertEqual(t, "config.content_type", "json", body.Config["content_type"])
		assertEqual(t, "config.secret", "s3cret", body.Config["secret"])
		assertEqual(t, "config.insecure_ssl", "0", body.Config["insecure_ssl"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id": 3, "active": true, "events": ["push", "issues"],
			"config": {"url": "https://example.com/hook", "content_type": "json", "insecure_ssl": "0"}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestGitHubWebhookService(srv)
	hook, err := svc.Create(context.Background(), "octocat", "hello", forge.CreateWebhookOpts{
		URL:    "https://example.com/hook",
		Secret: "s3cret",
		Events: []string{"push", "issues"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook.ID != 3 {
		t.Errorf("ID: want 3, got %d", hook.ID)
	}
	assertSliceEqual(t, "Events", []string{"push", "issues"}, hook.Events)
}

func TestGitHubWebhookDelete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v3/repos/octocat/hello/hooks/1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestGitHubWebhookService(srv)
	if err := svc.Delete(context.Background(), "octocat", "hello", 1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitHubWebhookDeleteNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/v3/repos/octocat/hello/hooks/999", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"message": "Not Found"}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestGitHubWebhookService(srv)
	err := svc.Delete(context.Background(), "octocat", "hello", 999)
	if err != forge.ErrNotFound {
		t.Fatalf("expected forge.ErrNotFound, got %v", err)
	}
}
