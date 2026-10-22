package github

import (
	"encoding/json"
	"errors"
	"fmt"
	forge "github.com/git-pkgs/forge"
	"net/http"

	"github.com/git-pkgs/forge/internal/hookutil"
	"github.com/google/go-github/v82/github"
)

// ParseWebhook verifies and parses a GitHub webhook delivery. When secret is
// non-empty the X-Hub-Signature-256 header (or the older X-Hub-Signature)
// must match the raw request body.
func ParseWebhook(r *http.Request, secret string) (*forge.WebhookEvent, error) {
	body, err := hookutil.ReadBody(r)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	if secret != "" {
		signature := hookutil.FirstHeader(r, github.SHA256SignatureHeader, github.SHA1SignatureHeader)
		if signature == "" || github.ValidateSignature(signature, body, []byte(secret)) != nil {
			return nil, forge.ErrInvalidWebhookSignature
		}
	}

	eventType := github.WebHookType(r)
	if eventType == "" {
		return nil, errors.New("github: webhook request has no X-GitHub-Event header")
	}

	payload, err := hookutil.Payload(r.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	return parseGitHubEvent(eventType, github.DeliveryID(r), payload)
}

func (s *gitHubWebhookService) ParseEvent(r *http.Request, secret string) (*forge.WebhookEvent, error) {
	return ParseWebhook(r, secret)
}

func parseGitHubEvent(eventType, deliveryID string, payload []byte) (*forge.WebhookEvent, error) {
	var envelope struct {
		Action     string             `json:"action"`
		Repository *github.Repository `json:"repository"`
		Sender     *github.User       `json:"sender"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("github: decoding webhook payload: %w", err)
	}

	event := &forge.WebhookEvent{
		Type:       eventType,
		Action:     envelope.Action,
		DeliveryID: deliveryID,
		Payload:    json.RawMessage(payload),
	}
	if envelope.Repository != nil {
		repo := convertGitHubRepo(envelope.Repository)
		event.Repository = &repo
	}
	if u := envelope.Sender; u != nil {
		event.Sender = &forge.User{
			Login:     u.GetLogin(),
			AvatarURL: u.GetAvatarURL(),
			HTMLURL:   u.GetHTMLURL(),
		}
	}

	switch eventType {
	case "push":
		var e github.PushEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("github: decoding push payload: %w", err)
		}
		event.Push = convertGitHubPush(&e)
	case "pull_request":
		var e github.PullRequestEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("github: decoding pull_request payload: %w", err)
		}
		if pr := e.GetPullRequest(); pr != nil {
			result := convertGitHubPR(pr)
			event.PullRequest = &result
		}
	case "issues":
		var e github.IssuesEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("github: decoding issues payload: %w", err)
		}
		if i := e.GetIssue(); i != nil {
			result := convertGitHubIssue(i)
			event.Issue = &result
		}
	case "issue_comment":
		var e github.IssueCommentEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("github: decoding issue_comment payload: %w", err)
		}
		if c := e.GetComment(); c != nil {
			result := convertGitHubComment(c)
			event.Comment = &result
		}
		if i := e.GetIssue(); i != nil {
			issue := convertGitHubIssue(i)
			if i.IsPullRequest() {
				pr := hookutil.IssueAsPullRequest(issue)
				event.PullRequest = &pr
			} else {
				event.Issue = &issue
			}
		}
	case "release":
		var e github.ReleaseEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("github: decoding release payload: %w", err)
		}
		if rel := e.GetRelease(); rel != nil {
			result := convertGitHubRelease(rel)
			event.Release = &result
		}
	}

	return event, nil
}

func convertGitHubPush(e *github.PushEvent) *forge.WebhookPush {
	push := &forge.WebhookPush{
		Ref:     e.GetRef(),
		Before:  e.GetBefore(),
		After:   e.GetAfter(),
		Created: e.GetCreated(),
		Deleted: e.GetDeleted(),
	}
	for _, c := range e.Commits {
		commit := forge.WebhookCommit{
			SHA:     c.GetID(),
			Message: c.GetMessage(),
			URL:     c.GetURL(),
		}
		if a := c.GetAuthor(); a != nil {
			commit.AuthorName = a.GetName()
			commit.AuthorEmail = a.GetEmail()
		}
		if t := c.GetTimestamp(); !t.IsZero() {
			commit.Timestamp = t.Time
		}
		push.Commits = append(push.Commits, commit)
	}
	return push
}
