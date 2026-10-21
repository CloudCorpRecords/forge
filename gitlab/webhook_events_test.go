//go:build !tinygo

package gitlab

import (
	"errors"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testWebhookToken = "s3cret"

func newGitLabWebhookRequest(event, payload string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gitlab-Event", event)
	r.Header.Set("X-Gitlab-Event-UUID", "delivery-1")
	r.Header.Set("X-Gitlab-Token", testWebhookToken)
	return r
}

const gitLabProjectJSON = `"project": {"name": "Hello", "path_with_namespace": "group/sub/hello",
	"web_url": "https://gitlab.example.com/group/sub/hello", "git_http_url": "https://gitlab.example.com/group/sub/hello.git",
	"git_ssh_url": "git@gitlab.example.com:group/sub/hello.git", "default_branch": "main", "visibility_level": 0}`

func parseGitLabTestEvent(t *testing.T, event, payload string) *forge.WebhookEvent {
	t.Helper()
	ev, err := ParseWebhook(newGitLabWebhookRequest(event, payload), testWebhookToken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return ev
}

func TestGitLabParseWebhookPush(t *testing.T) {
	payload := `{"object_kind": "push", "before": "0000000000000000000000000000000000000000", "after": "abc123",
		"ref": "refs/heads/main", "user_username": "alice", "user_name": "Alice", "user_avatar": "https://avatars.example.com/alice",
		` + gitLabProjectJSON + `,
		"commits": [{"id": "abc123", "message": "Fix bug", "timestamp": "2024-01-15T12:00:00+02:00",
			"url": "https://gitlab.example.com/group/sub/hello/-/commit/abc123",
			"author": {"name": "Alice", "email": "alice@example.com"}}]}`
	event := parseGitLabTestEvent(t, "Push Hook", payload)

	assertEqual(t, "Type", "push", event.Type)
	assertEqual(t, "DeliveryID", "delivery-1", event.DeliveryID)
	if event.Repository == nil {
		t.Fatal("Repository is nil")
	}
	assertEqual(t, "Repository.FullName", "group/sub/hello", event.Repository.FullName)
	assertEqual(t, "Repository.Owner", "sub", event.Repository.Owner)
	assertEqual(t, "Repository.Name", "Hello", event.Repository.Name)
	assertEqualBool(t, "Repository.Private", true, event.Repository.Private)
	if event.Sender == nil || event.Sender.Login != "alice" || event.Sender.Name != "Alice" {
		t.Fatalf("Sender = %+v", event.Sender)
	}
	if event.Push == nil {
		t.Fatal("Push is nil")
	}
	assertEqualBool(t, "Push.Created", true, event.Push.Created)
	if len(event.Push.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(event.Push.Commits))
	}
	c := event.Push.Commits[0]
	assertEqual(t, "Commit.SHA", "abc123", c.SHA)
	assertEqual(t, "Commit.AuthorEmail", "alice@example.com", c.AuthorEmail)
	if !c.Timestamp.Equal(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Commit.Timestamp = %v", c.Timestamp)
	}
}

func TestGitLabParseWebhookTagPush(t *testing.T) {
	payload := `{"object_kind": "tag_push", "before": "0000000000000000000000000000000000000000",
		"after": "abc123", "ref": "refs/tags/v1.0.0", "user_username": "alice", ` + gitLabProjectJSON + `}`
	event := parseGitLabTestEvent(t, "Tag Push Hook", payload)
	assertEqual(t, "Type", "tag_push", event.Type)
	if event.Push == nil || event.Push.Ref != "refs/tags/v1.0.0" {
		t.Fatalf("Push = %+v", event.Push)
	}
}

func TestGitLabParseWebhookToken(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*http.Request)
		secret    string
		wantValid bool
	}{
		{"valid", func(*http.Request) {}, testWebhookToken, true},
		{"wrong secret", func(*http.Request) {}, "other", false},
		{"missing token", func(r *http.Request) { r.Header.Del("X-Gitlab-Token") }, testWebhookToken, false},
		{"wrong token", func(r *http.Request) { r.Header.Set("X-Gitlab-Token", "s3cre") }, testWebhookToken, false},
		{"no secret skips verification", func(r *http.Request) { r.Header.Del("X-Gitlab-Token") }, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGitLabWebhookRequest("Push Hook", `{"ref": "refs/heads/main"}`)
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

func TestGitLabParseWebhookMergeRequest(t *testing.T) {
	mr := func(action, state, oldrev string) string {
		return `{"object_kind": "merge_request", "user": {"username": "bob", "name": "Bob"}, ` + gitLabProjectJSON + `,
			"object_attributes": {"iid": 42, "title": "Add feature", "description": "Details", "state": "` + state + `",
				"action": "` + action + `", "oldrev": "` + oldrev + `", "url": "https://gitlab.example.com/group/sub/hello/-/merge_requests/42",
				"source_branch": "feature", "target_branch": "main", "draft": true, "last_commit": {"id": "def456"},
				"created_at": "2024-01-15 10:00:00 UTC", "updated_at": "2024-01-15T11:00:00Z",
				"source": {"path_with_namespace": "fork/hello", "name": "hello", "git_http_url": "https://gitlab.example.com/fork/hello.git"},
				"target": {"path_with_namespace": "group/sub/hello", "name": "hello"}},
			"labels": [{"title": "feature", "color": "#00ff00"}],
			"assignees": [{"username": "carol"}], "reviewers": [{"username": "dave"}]}`
	}

	tests := []struct {
		name       string
		action     string
		state      string
		oldrev     string
		wantAction string
		wantState  forge.PRStatus
		wantMerged bool
	}{
		{"open", "open", "opened", "", "opened", forge.PRStatusOpen, false},
		{"edit", "update", "opened", "", "edited", forge.PRStatusOpen, false},
		{"new commits", "update", "opened", "aaa111", "synchronize", forge.PRStatusOpen, false},
		{"close", "close", "closed", "", "closed", forge.PRStatusClosed, false},
		{"reopen", "reopen", "opened", "", "reopened", forge.PRStatusOpen, false},
		{"merge", "merge", "merged", "", "closed", forge.PRStatusMerged, true},
		{"approval kept as sent", "approved", "opened", "", "approved", forge.PRStatusOpen, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := parseGitLabTestEvent(t, "Merge Request Hook", mr(tt.action, tt.state, tt.oldrev))
			assertEqual(t, "Type", "pull_request", event.Type)
			assertEqual(t, "Action", tt.wantAction, event.Action)
			pr := event.PullRequest
			if pr == nil {
				t.Fatal("PullRequest is nil")
			}
			assertEqual(t, "State", string(tt.wantState), pr.State)
			assertEqualBool(t, "Merged", tt.wantMerged, pr.Merged)
			if pr.Number != 42 {
				t.Errorf("Number = %d, want 42", pr.Number)
			}
			assertEqual(t, "Head.Ref", "feature", pr.Head.Ref)
			assertEqual(t, "Head.SHA", "def456", pr.Head.SHA)
			assertEqual(t, "Base.Ref", "main", pr.Base.Ref)
			assertEqualBool(t, "Draft", true, pr.Draft)
			if pr.Head.Fork == nil || pr.Head.Fork.Owner != "fork" {
				t.Errorf("Head.Fork = %+v", pr.Head.Fork)
			}
			if len(pr.Labels) != 1 || pr.Labels[0].Name != "feature" {
				t.Errorf("Labels = %+v", pr.Labels)
			}
			if len(pr.Assignees) != 1 || pr.Assignees[0].Login != "carol" {
				t.Errorf("Assignees = %+v", pr.Assignees)
			}
			if len(pr.Reviewers) != 1 || pr.Reviewers[0].Login != "dave" {
				t.Errorf("Reviewers = %+v", pr.Reviewers)
			}
			if !pr.CreatedAt.Equal(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)) {
				t.Errorf("CreatedAt = %v", pr.CreatedAt)
			}
			if !pr.UpdatedAt.Equal(time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)) {
				t.Errorf("UpdatedAt = %v", pr.UpdatedAt)
			}
			if event.Sender == nil || event.Sender.Login != "bob" {
				t.Errorf("Sender = %+v", event.Sender)
			}
		})
	}
}

func TestGitLabParseWebhookIssue(t *testing.T) {
	for _, header := range []string{"Issue Hook", "Confidential Issue Hook"} {
		t.Run(header, func(t *testing.T) {
			payload := `{"object_kind": "issue", "user": {"username": "alice"}, ` + gitLabProjectJSON + `,
				"object_attributes": {"iid": 7, "title": "Bug", "description": "Broken", "state": "closed", "action": "close",
					"url": "https://gitlab.example.com/group/sub/hello/-/issues/7", "closed_at": "2024-01-16 09:00:00 +0000"},
				"labels": [{"title": "bug"}]}`
			event := parseGitLabTestEvent(t, header, payload)
			assertEqual(t, "Type", "issues", event.Type)
			assertEqual(t, "Action", "closed", event.Action)
			if event.Issue == nil || event.Issue.Number != 7 {
				t.Fatalf("Issue = %+v", event.Issue)
			}
			assertEqual(t, "State", "closed", event.Issue.State)
			if event.Issue.ClosedAt == nil || !event.Issue.ClosedAt.Equal(time.Date(2024, 1, 16, 9, 0, 0, 0, time.UTC)) {
				t.Errorf("ClosedAt = %v", event.Issue.ClosedAt)
			}
			if len(event.Issue.Labels) != 1 || event.Issue.Labels[0].Name != "bug" {
				t.Errorf("Labels = %+v", event.Issue.Labels)
			}
		})
	}
}

func TestGitLabParseWebhookNote(t *testing.T) {
	note := func(noteableType, target string) string {
		return `{"object_kind": "note", "user": {"username": "carol", "name": "Carol"}, ` + gitLabProjectJSON + `,
			"object_attributes": {"id": 99, "note": "LGTM", "noteable_type": "` + noteableType + `",
				"url": "https://gitlab.example.com/group/sub/hello/-/issues/7#note_99",
				"created_at": "2024-01-15 10:00:00 UTC"}` + target + `}`
	}

	t.Run("on issue", func(t *testing.T) {
		event := parseGitLabTestEvent(t, "Note Hook", note("Issue", `, "issue": {"iid": 7, "title": "Bug", "state": "opened"}`))
		assertEqual(t, "Type", "issue_comment", event.Type)
		assertEqual(t, "Action", "created", event.Action)
		if event.Comment == nil || event.Comment.ID != 99 || event.Comment.Body != "LGTM" {
			t.Fatalf("Comment = %+v", event.Comment)
		}
		assertEqual(t, "Comment.Author", "carol", event.Comment.Author.Login)
		if event.Issue == nil || event.Issue.Number != 7 || event.PullRequest != nil {
			t.Fatalf("Issue = %+v, PullRequest = %+v", event.Issue, event.PullRequest)
		}
	})

	t.Run("on merge request", func(t *testing.T) {
		event := parseGitLabTestEvent(t, "Note Hook", note("MergeRequest",
			`, "merge_request": {"iid": 42, "title": "Feature", "state": "opened", "source_branch": "feature", "target_branch": "main"}`))
		assertEqual(t, "Type", "issue_comment", event.Type)
		if event.PullRequest == nil || event.PullRequest.Number != 42 || event.Issue != nil {
			t.Fatalf("PullRequest = %+v, Issue = %+v", event.PullRequest, event.Issue)
		}
		assertEqual(t, "Head.Ref", "feature", event.PullRequest.Head.Ref)
	})

	t.Run("edited", func(t *testing.T) {
		payload := strings.Replace(note("Issue", `, "issue": {"iid": 7}`), `"noteable_type"`, `"action": "update", "noteable_type"`, 1)
		event := parseGitLabTestEvent(t, "Note Hook", payload)
		assertEqual(t, "Action", "edited", event.Action)
	})

	t.Run("on commit", func(t *testing.T) {
		event := parseGitLabTestEvent(t, "Note Hook", note("Commit", `, "commit": {"id": "abc123"}`))
		assertEqual(t, "Type", "commit_comment", event.Type)
		if event.Comment != nil {
			t.Errorf("Comment set for commit note: %+v", event.Comment)
		}
	})

	t.Run("on snippet keeps header", func(t *testing.T) {
		event := parseGitLabTestEvent(t, "Confidential Note Hook", note("Snippet", ""))
		assertEqual(t, "Type", "Confidential Note Hook", event.Type)
	})
}

func TestGitLabParseWebhookRelease(t *testing.T) {
	release := func(action string) string {
		return `{"object_kind": "release", "action": "` + action + `", "name": "First", "tag": "v1.0.0",
			"description": "Notes", "url": "https://gitlab.example.com/group/sub/hello/-/releases/v1.0.0",
			"created_at": "2024-01-15 10:00:00 UTC", "released_at": "2024-01-15 10:00:00 UTC",
			"commit": {"id": "abc123"}, ` + gitLabProjectJSON + `,
			"assets": {"links": [{"id": 1, "name": "binary", "url": "https://example.com/binary"}],
				"sources": [{"format": "zip", "url": "https://example.com/v1.zip"}, {"format": "tar.gz", "url": "https://example.com/v1.tar.gz"}]}}`
	}
	for action, want := range map[string]string{"create": "published", "update": "edited", "delete": "deleted"} {
		t.Run(action, func(t *testing.T) {
			event := parseGitLabTestEvent(t, "Release Hook", release(action))
			assertEqual(t, "Type", "release", event.Type)
			assertEqual(t, "Action", want, event.Action)
			rel := event.Release
			if rel == nil {
				t.Fatal("Release is nil")
			}
			assertEqual(t, "TagName", "v1.0.0", rel.TagName)
			assertEqual(t, "Title", "First", rel.Title)
			assertEqual(t, "Target", "abc123", rel.Target)
			assertEqual(t, "ZipballURL", "https://example.com/v1.zip", rel.ZipballURL)
			assertEqual(t, "TarballURL", "https://example.com/v1.tar.gz", rel.TarballURL)
			if len(rel.Assets) != 1 || rel.Assets[0].Name != "binary" {
				t.Errorf("Assets = %+v", rel.Assets)
			}
		})
	}
}

func TestGitLabParseWebhookEventNames(t *testing.T) {
	for header, want := range map[string]string{
		"Pipeline Hook":   "workflow_run",
		"Job Hook":        "workflow_job",
		"Deployment Hook": "deployment",
		"Wiki Page Hook":  "gollum",
		"Emoji Hook":      "Emoji Hook",
	} {
		t.Run(header, func(t *testing.T) {
			event := parseGitLabTestEvent(t, header, `{"user": {"username": "alice"}, `+gitLabProjectJSON+`}`)
			assertEqual(t, "Type", want, event.Type)
			if event.Repository == nil || event.Sender == nil {
				t.Errorf("Repository = %+v, Sender = %+v", event.Repository, event.Sender)
			}
		})
	}
}

func TestGitLabParseWebhookErrors(t *testing.T) {
	r := newGitLabWebhookRequest("Push Hook", `{}`)
	r.Header.Del("X-Gitlab-Event")
	if _, err := ParseWebhook(r, testWebhookToken); err == nil {
		t.Error("expected error for missing event header")
	}

	if _, err := ParseWebhook(newGitLabWebhookRequest("Push Hook", `not json`), testWebhookToken); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGitLabWebhookServiceParseEvent(t *testing.T) {
	f := New("https://gitlab.example.com", "", nil)
	event, err := f.Webhooks().ParseEvent(newGitLabWebhookRequest("Push Hook", `{"ref": "refs/heads/main"}`), testWebhookToken)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEqual(t, "Type", "push", event.Type)
}
