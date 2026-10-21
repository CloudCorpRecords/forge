package gitea

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const testWebhookSecret = "s3cret"

func signGiteaPayload(payload string) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func newGiteaWebhookRequest(event, payload string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gitea-Event", event)
	r.Header.Set("X-Gitea-Delivery", "delivery-1")
	r.Header.Set("X-Gitea-Signature", signGiteaPayload(payload))
	return r
}

const giteaRepoJSON = `"repository": {"full_name": "octocat/hello", "name": "hello", "owner": {"login": "octocat"},
	"html_url": "https://gitea.example.com/octocat/hello"}`

func TestGiteaParseWebhookPush(t *testing.T) {
	payload := `{"ref": "refs/heads/main",
		"before": "0000000000000000000000000000000000000000", "after": "abc123",
		"commits": [{"id": "abc123", "message": "Fix bug", "url": "https://gitea.example.com/octocat/hello/commit/abc123",
			"timestamp": "2024-01-15T10:00:00Z", "author": {"name": "Octo Cat", "email": "octo@example.com"}}],
		` + giteaRepoJSON + `, "sender": {"login": "octocat", "full_name": "Octo Cat"}}`

	event, err := ParseWebhook(newGiteaWebhookRequest("push", payload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "push", event.Type)
	assertEqual(t, "DeliveryID", "delivery-1", event.DeliveryID)
	if event.Repository == nil || event.Repository.FullName != "octocat/hello" || event.Repository.Owner != "octocat" {
		t.Fatalf("Repository = %+v", event.Repository)
	}
	if event.Sender == nil || event.Sender.Login != "octocat" || event.Sender.Name != "Octo Cat" {
		t.Fatalf("Sender = %+v", event.Sender)
	}
	if event.Push == nil {
		t.Fatal("Push is nil")
	}
	assertEqualBool(t, "Push.Created", true, event.Push.Created)
	assertEqualBool(t, "Push.Deleted", false, event.Push.Deleted)
	if len(event.Push.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(event.Push.Commits))
	}
	assertEqual(t, "Commit.SHA", "abc123", event.Push.Commits[0].SHA)
	assertEqual(t, "Commit.AuthorEmail", "octo@example.com", event.Push.Commits[0].AuthorEmail)
}

func TestGiteaParseWebhookSignature(t *testing.T) {
	payload := `{"ref": "refs/heads/main", "before": "a", "after": "b"}`
	tests := []struct {
		name      string
		mutate    func(*http.Request)
		secret    string
		wantValid bool
	}{
		{"valid", func(*http.Request) {}, testWebhookSecret, true},
		{"wrong secret", func(*http.Request) {}, "other", false},
		{"missing signature", func(r *http.Request) { r.Header.Del("X-Gitea-Signature") }, testWebhookSecret, false},
		{"tampered signature", func(r *http.Request) { r.Header.Set("X-Gitea-Signature", "00") }, testWebhookSecret, false},
		{"forgejo header", func(r *http.Request) {
			r.Header.Set("X-Forgejo-Signature", r.Header.Get("X-Gitea-Signature"))
			r.Header.Del("X-Gitea-Signature")
		}, testWebhookSecret, true},
		{"no secret skips verification", func(r *http.Request) { r.Header.Del("X-Gitea-Signature") }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGiteaWebhookRequest("push", payload)
			tt.mutate(r)
			_, err := ParseWebhook(r, tt.secret)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, forge.ErrInvalidWebhookSignature) {
				t.Fatalf("expected ErrInvalidWebhookSignature, got %v", err)
			}
		})
	}
}

func TestGiteaParseWebhookFormSignsPayload(t *testing.T) {
	payload := `{"ref": "refs/heads/main", "before": "a", "after": "b"}`
	body := "payload=" + url.QueryEscape(payload)

	tests := []struct {
		name      string
		signature string
		wantValid bool
	}{
		// Gitea and Forgejo sign the JSON payload, not the form body.
		{"signed payload", signGiteaPayload(payload), true},
		{"signed form body", signGiteaPayload(body), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("X-Gitea-Event", "push")
			r.Header.Set("X-Gitea-Signature", tt.signature)

			event, err := ParseWebhook(r, testWebhookSecret)
			if !tt.wantValid {
				if !errors.Is(err, forge.ErrInvalidWebhookSignature) {
					t.Fatalf("expected ErrInvalidWebhookSignature, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if event.Push == nil || event.Push.Ref != "refs/heads/main" {
				t.Fatalf("Push = %+v", event.Push)
			}
		})
	}
}

func TestGiteaParseWebhookPrefersForgejoHeaders(t *testing.T) {
	payload := `{"action": "created"}`
	r := newGiteaWebhookRequest("push", payload)
	r.Header.Set("X-Forgejo-Event", "wiki")
	r.Header.Set("X-Forgejo-Delivery", "forgejo-1")

	event, err := ParseWebhook(r, testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "gollum", event.Type)
	assertEqual(t, "DeliveryID", "forgejo-1", event.DeliveryID)
}

func TestGiteaParseWebhookPullRequest(t *testing.T) {
	payload := `{"action": "synchronized", "number": 42,
		"pull_request": {"number": 42, "title": "Add feature", "state": "open",
			"html_url": "https://gitea.example.com/octocat/hello/pulls/42", "user": {"login": "alice"},
			"head": {"ref": "feature", "sha": "def456"}, "base": {"ref": "main"}},
		` + giteaRepoJSON + `}`
	event, err := ParseWebhook(newGiteaWebhookRequest("pull_request", payload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "pull_request", event.Type)
	assertEqual(t, "Action", "synchronize", event.Action)
	if event.PullRequest == nil || event.PullRequest.Number != 42 {
		t.Fatalf("PullRequest = %+v", event.PullRequest)
	}
	assertEqual(t, "Head.SHA", "def456", event.PullRequest.Head.SHA)
	assertEqual(t, "State", "open", event.PullRequest.State)
}

func TestGiteaParseWebhookMergedPullRequest(t *testing.T) {
	payload := `{"action": "closed", "pull_request": {"number": 42, "state": "closed", "merged": true}}`
	event, err := ParseWebhook(newGiteaWebhookRequest("pull_request", payload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Action", "closed", event.Action)
	assertEqualBool(t, "Merged", true, event.PullRequest.Merged)
	assertEqual(t, "State", "merged", event.PullRequest.State)
}

func TestGiteaParseWebhookIssues(t *testing.T) {
	payload := `{"action": "opened", "number": 7,
		"issue": {"number": 7, "title": "Bug", "state": "open", "user": {"login": "alice"}}}`
	event, err := ParseWebhook(newGiteaWebhookRequest("issues", payload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Action", "opened", event.Action)
	if event.Issue == nil || event.Issue.Number != 7 || event.Issue.Title != "Bug" {
		t.Fatalf("Issue = %+v", event.Issue)
	}
	assertEqual(t, "Author", "alice", event.Issue.Author.Login)
}

func TestGiteaParseWebhookIssueComment(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantPR  bool
	}{
		{"on issue", `{"action": "created", "is_pull": false,
			"issue": {"number": 7, "title": "Bug", "state": "open"},
			"comment": {"id": 99, "body": "Same here", "user": {"login": "carol"}}}`, false},
		{"on pull request", `{"action": "created", "is_pull": true,
			"issue": {"number": 42, "title": "Feature", "state": "open", "pull_request": {"merged": false}},
			"comment": {"id": 99, "body": "Same here", "user": {"login": "carol"}}}`, true},
		{"on pull request with pull_request object", `{"action": "created", "is_pull": true,
			"issue": {"number": 42, "title": "Feature", "state": "open"},
			"pull_request": {"number": 42, "title": "Feature", "state": "open", "head": {"ref": "feature"}},
			"comment": {"id": 99, "body": "Same here", "user": {"login": "carol"}}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := ParseWebhook(newGiteaWebhookRequest("issue_comment", tt.payload), testWebhookSecret)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertEqual(t, "Action", "created", event.Action)
			if event.Comment == nil || event.Comment.ID != 99 || event.Comment.Body != "Same here" {
				t.Fatalf("Comment = %+v", event.Comment)
			}
			assertEqual(t, "Comment.Author", "carol", event.Comment.Author.Login)
			if tt.wantPR {
				if event.PullRequest == nil || event.PullRequest.Number != 42 || event.Issue != nil {
					t.Fatalf("PullRequest = %+v, Issue = %+v", event.PullRequest, event.Issue)
				}
				return
			}
			if event.Issue == nil || event.Issue.Number != 7 || event.PullRequest != nil {
				t.Fatalf("Issue = %+v, PullRequest = %+v", event.Issue, event.PullRequest)
			}
		})
	}
}

func TestGiteaParseWebhookRelease(t *testing.T) {
	payload := `{"action": "updated",
		"release": {"tag_name": "v1.0.0", "name": "First", "body": "Notes", "author": {"login": "alice"}}}`
	event, err := ParseWebhook(newGiteaWebhookRequest("release", payload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Action", "edited", event.Action)
	if event.Release == nil || event.Release.TagName != "v1.0.0" || event.Release.Title != "First" {
		t.Fatalf("Release = %+v", event.Release)
	}
	assertEqual(t, "Release.Body", "Notes", event.Release.Body)
}

func TestGiteaParseWebhookEventNames(t *testing.T) {
	for header, want := range map[string]string{
		"wiki":                  "gollum",
		"pull_request_approved": "pull_request_review",
		"pull_request_rejected": "pull_request_review",
		"pull_request_comment":  "pull_request_review",
		"create":                "create",
		"repository":            "repository",
	} {
		t.Run(header, func(t *testing.T) {
			event, err := ParseWebhook(newGiteaWebhookRequest(header, `{}`), testWebhookSecret)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertEqual(t, "Type", want, event.Type)
		})
	}
}

func TestGiteaParseWebhookErrors(t *testing.T) {
	r := newGiteaWebhookRequest("push", `{}`)
	r.Header.Del("X-Gitea-Event")
	if _, err := ParseWebhook(r, testWebhookSecret); err == nil {
		t.Error("expected error for missing event header")
	}

	if _, err := ParseWebhook(newGiteaWebhookRequest("push", `not json`), testWebhookSecret); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGiteaWebhookServiceParseEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)
	event, err := f.Webhooks().ParseEvent(newGiteaWebhookRequest("push", `{"ref": "refs/heads/main"}`), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "push", event.Type)
}
