package github

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

func signGitHubPayload(body string) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newGitHubWebhookRequest(event, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", "delivery-1")
	r.Header.Set("X-Hub-Signature-256", signGitHubPayload(body))
	return r
}

const gitHubPushPayload = `{
	"ref": "refs/heads/main",
	"before": "0000000000000000000000000000000000000000",
	"after": "abc123",
	"created": true,
	"deleted": false,
	"commits": [
		{"id": "abc123", "message": "Fix bug", "url": "https://github.com/octocat/hello/commit/abc123",
		 "timestamp": "2024-01-15T10:00:00Z", "author": {"name": "Octo Cat", "email": "octo@example.com"}}
	],
	"repository": {"full_name": "octocat/hello", "name": "hello", "owner": {"login": "octocat"},
		"html_url": "https://github.com/octocat/hello", "created_at": 1705312800},
	"sender": {"login": "octocat", "avatar_url": "https://avatars.example.com/octocat"}
}`

func TestGitHubParseWebhookPush(t *testing.T) {
	event, err := ParseWebhook(newGitHubWebhookRequest("push", gitHubPushPayload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertEqual(t, "Type", "push", event.Type)
	assertEqual(t, "DeliveryID", "delivery-1", event.DeliveryID)
	if event.Repository == nil || event.Repository.FullName != "octocat/hello" || event.Repository.Owner != "octocat" {
		t.Fatalf("Repository = %+v", event.Repository)
	}
	if event.Sender == nil || event.Sender.Login != "octocat" {
		t.Fatalf("Sender = %+v", event.Sender)
	}
	if event.Push == nil {
		t.Fatal("Push is nil")
	}
	assertEqual(t, "Push.Ref", "refs/heads/main", event.Push.Ref)
	assertEqual(t, "Push.After", "abc123", event.Push.After)
	assertEqualBool(t, "Push.Created", true, event.Push.Created)
	assertEqualBool(t, "Push.Deleted", false, event.Push.Deleted)
	if len(event.Push.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(event.Push.Commits))
	}
	c := event.Push.Commits[0]
	assertEqual(t, "Commit.SHA", "abc123", c.SHA)
	assertEqual(t, "Commit.Message", "Fix bug", c.Message)
	assertEqual(t, "Commit.AuthorName", "Octo Cat", c.AuthorName)
	assertEqual(t, "Commit.AuthorEmail", "octo@example.com", c.AuthorEmail)
	if !c.Timestamp.Equal(parseTime("2024-01-15T10:00:00Z")) {
		t.Errorf("Commit.Timestamp = %v", c.Timestamp)
	}
	if string(event.Payload) != gitHubPushPayload {
		t.Error("Payload does not hold the raw body")
	}
}

func TestGitHubParseWebhookSignature(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*http.Request)
		secret    string
		wantValid bool
	}{
		{"valid", func(*http.Request) {}, testWebhookSecret, true},
		{"wrong secret", func(*http.Request) {}, "other", false},
		{"missing signature", func(r *http.Request) { r.Header.Del("X-Hub-Signature-256") }, testWebhookSecret, false},
		{"tampered signature", func(r *http.Request) { r.Header.Set("X-Hub-Signature-256", "sha256=00") }, testWebhookSecret, false},
		{"no secret skips verification", func(r *http.Request) { r.Header.Del("X-Hub-Signature-256") }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGitHubWebhookRequest("push", gitHubPushPayload)
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

func TestGitHubParseWebhookForm(t *testing.T) {
	body := "payload=" + url.QueryEscape(gitHubPushPayload)
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-GitHub-Event", "push")
	// GitHub signs the raw form body, not the JSON inside it.
	r.Header.Set("X-Hub-Signature-256", signGitHubPayload(body))

	event, err := ParseWebhook(r, testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Push == nil || event.Push.Ref != "refs/heads/main" {
		t.Fatalf("Push = %+v", event.Push)
	}
}

func TestGitHubParseWebhookPullRequest(t *testing.T) {
	body := `{"action": "closed", "number": 42,
		"pull_request": {"number": 42, "title": "Add feature", "state": "closed", "merged": true,
			"html_url": "https://github.com/octocat/hello/pull/42", "user": {"login": "alice"},
			"head": {"ref": "feature", "sha": "def456"}, "base": {"ref": "main"}},
		"repository": {"full_name": "octocat/hello", "owner": {"login": "octocat"}},
		"sender": {"login": "bob"}}`
	event, err := ParseWebhook(newGitHubWebhookRequest("pull_request", body), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "pull_request", event.Type)
	assertEqual(t, "Action", "closed", event.Action)
	if event.PullRequest == nil {
		t.Fatal("PullRequest is nil")
	}
	if event.PullRequest.Number != 42 {
		t.Errorf("Number = %d, want 42", event.PullRequest.Number)
	}
	assertEqualBool(t, "Merged", true, event.PullRequest.Merged)
	assertEqual(t, "State", "merged", event.PullRequest.State)
	assertEqual(t, "Head.Ref", "feature", event.PullRequest.Head.Ref)
	assertEqual(t, "Author", "alice", event.PullRequest.Author.Login)
}

func TestGitHubParseWebhookIssues(t *testing.T) {
	body := `{"action": "opened",
		"issue": {"number": 7, "title": "Bug", "state": "open", "user": {"login": "alice"},
			"labels": [{"name": "bug"}]},
		"repository": {"full_name": "octocat/hello", "owner": {"login": "octocat"}}}`
	event, err := ParseWebhook(newGitHubWebhookRequest("issues", body), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Action", "opened", event.Action)
	if event.Issue == nil || event.Issue.Number != 7 {
		t.Fatalf("Issue = %+v", event.Issue)
	}
	assertEqual(t, "State", "open", event.Issue.State)
	if len(event.Issue.Labels) != 1 || event.Issue.Labels[0].Name != "bug" {
		t.Errorf("Labels = %+v", event.Issue.Labels)
	}
}

func TestGitHubParseWebhookIssueComment(t *testing.T) {
	tests := []struct {
		name   string
		issue  string
		wantPR bool
	}{
		{"on issue", `{"number": 7, "title": "Bug", "state": "open"}`, false},
		{"on pull request", `{"number": 42, "title": "Feature", "state": "open",
			"pull_request": {"url": "https://api.github.com/repos/octocat/hello/pulls/42"}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"action": "created", "issue": ` + tt.issue + `,
				"comment": {"id": 99, "body": "LGTM", "user": {"login": "carol"}}}`
			event, err := ParseWebhook(newGitHubWebhookRequest("issue_comment", body), testWebhookSecret)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if event.Comment == nil || event.Comment.ID != 99 || event.Comment.Body != "LGTM" {
				t.Fatalf("Comment = %+v", event.Comment)
			}
			assertEqual(t, "Comment.Author", "carol", event.Comment.Author.Login)
			if tt.wantPR {
				if event.PullRequest == nil || event.PullRequest.Number != 42 || event.Issue != nil {
					t.Fatalf("PullRequest = %+v, Issue = %+v", event.PullRequest, event.Issue)
				}
				assertEqual(t, "PullRequest.State", "open", event.PullRequest.State)
				return
			}
			if event.Issue == nil || event.Issue.Number != 7 || event.PullRequest != nil {
				t.Fatalf("Issue = %+v, PullRequest = %+v", event.Issue, event.PullRequest)
			}
		})
	}
}

func TestGitHubParseWebhookRelease(t *testing.T) {
	body := `{"action": "published",
		"release": {"tag_name": "v1.0.0", "name": "First", "body": "Notes", "author": {"login": "alice"}}}`
	event, err := ParseWebhook(newGitHubWebhookRequest("release", body), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Action", "published", event.Action)
	if event.Release == nil || event.Release.TagName != "v1.0.0" || event.Release.Title != "First" {
		t.Fatalf("Release = %+v", event.Release)
	}
}

func TestGitHubParseWebhookUnknownEvent(t *testing.T) {
	body := `{"zen": "Keep it simple.", "hook_id": 1,
		"repository": {"full_name": "octocat/hello", "owner": {"login": "octocat"}},
		"sender": {"login": "octocat"}}`
	event, err := ParseWebhook(newGitHubWebhookRequest("ping", body), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "ping", event.Type)
	if event.Repository == nil || event.Sender == nil {
		t.Fatalf("Repository = %+v, Sender = %+v", event.Repository, event.Sender)
	}
	if event.Push != nil || event.PullRequest != nil || event.Issue != nil || event.Comment != nil || event.Release != nil {
		t.Errorf("typed fields set for unknown event: %+v", event)
	}
}

func TestGitHubParseWebhookErrors(t *testing.T) {
	r := newGitHubWebhookRequest("push", gitHubPushPayload)
	r.Header.Del("X-GitHub-Event")
	if _, err := ParseWebhook(r, testWebhookSecret); err == nil {
		t.Error("expected error for missing event header")
	}

	if _, err := ParseWebhook(newGitHubWebhookRequest("push", `not json`), testWebhookSecret); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGitHubWebhookServiceParseEvent(t *testing.T) {
	f := New("", nil)
	event, err := f.Webhooks().ParseEvent(newGitHubWebhookRequest("push", gitHubPushPayload), testWebhookSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "push", event.Type)
}
