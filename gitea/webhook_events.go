package gitea

import (
	"encoding/json"
	"errors"
	"fmt"
	forge "github.com/git-pkgs/forge"
	"net/http"

	"code.gitea.io/sdk/gitea"
	"github.com/git-pkgs/forge/internal/hookutil"
)

// giteaDeliveryEvents maps the event names Gitea/Forgejo send in the
// X-Gitea-Event header onto normalized names. The header already groups
// sub-events (issue_label arrives as issues, pull_request_sync as
// pull_request), so only the names that differ are listed.
var giteaDeliveryEvents = map[string]string{
	"wiki":                  "gollum",
	"pull_request_approved": "pull_request_review",
	"pull_request_rejected": "pull_request_review",
	"pull_request_comment":  "pull_request_review",
}

// giteaActions maps Gitea/Forgejo actions onto GitHub's names where they
// differ.
var giteaActions = map[string]string{
	"synchronized": "synchronize",
	"updated":      "edited",
}

// ParseWebhook verifies and parses a Gitea or Forgejo webhook delivery. When
// secret is non-empty the X-Forgejo-Signature or X-Gitea-Signature header must
// match the JSON payload, which is what both servers sign whether it arrives as
// a JSON body, a form body or a GET query parameter.
func ParseWebhook(r *http.Request, secret string) (*forge.WebhookEvent, error) {
	payload, err := giteaPayload(r)
	if err != nil {
		return nil, fmt.Errorf("gitea: %w", err)
	}

	if secret != "" {
		signature := hookutil.FirstHeader(r, "X-Forgejo-Signature", "X-Gitea-Signature", "X-Gogs-Signature")
		if signature == "" || !hookutil.ValidHMACSHA256(payload, secret, signature) {
			return nil, forge.ErrInvalidWebhookSignature
		}
	}

	eventType := hookutil.FirstHeader(r, "X-Forgejo-Event", "X-Gitea-Event", "X-Gogs-Event")
	if eventType == "" {
		return nil, errors.New("gitea: webhook request has no X-Gitea-Event header")
	}
	deliveryID := hookutil.FirstHeader(r, "X-Forgejo-Delivery", "X-Gitea-Delivery", "X-Gogs-Delivery")

	return parseGiteaEvent(eventType, deliveryID, payload)
}

// giteaPayload returns the JSON payload of a delivery. Hooks configured to use
// GET send it in the payload query parameter with no body.
func giteaPayload(r *http.Request) ([]byte, error) {
	if r.Method == http.MethodGet {
		payload := r.URL.Query().Get("payload")
		if payload == "" {
			return nil, errors.New("webhook GET request has no payload query parameter")
		}
		return []byte(payload), nil
	}

	body, err := hookutil.ReadBody(r)
	if err != nil {
		return nil, err
	}
	return hookutil.Payload(r.Header.Get("Content-Type"), body)
}

func (s *giteaWebhookService) ParseEvent(r *http.Request, secret string) (*forge.WebhookEvent, error) {
	return ParseWebhook(r, secret)
}

func parseGiteaEvent(eventType, deliveryID string, payload []byte) (*forge.WebhookEvent, error) {
	var envelope struct {
		Action     string            `json:"action"`
		Repository *gitea.Repository `json:"repository"`
		Sender     *gitea.User       `json:"sender"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("gitea: decoding webhook payload: %w", err)
	}

	event := &forge.WebhookEvent{
		Type:       eventType,
		Action:     envelope.Action,
		DeliveryID: deliveryID,
		Payload:    json.RawMessage(payload),
	}
	if name, ok := giteaDeliveryEvents[eventType]; ok {
		event.Type = name
	}
	if action, ok := giteaActions[envelope.Action]; ok {
		event.Action = action
	}
	if r := envelope.Repository; r != nil && r.Owner != nil {
		repo := convertGiteaRepo(r)
		event.Repository = &repo
	}
	if u := envelope.Sender; u != nil {
		event.Sender = convertGiteaHookUser(u)
	}

	switch eventType {
	case "push":
		var e struct {
			Ref     string                 `json:"ref"`
			Before  string                 `json:"before"`
			After   string                 `json:"after"`
			Commits []*gitea.PayloadCommit `json:"commits"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitea: decoding push payload: %w", err)
		}
		push := &forge.WebhookPush{
			Ref:     e.Ref,
			Before:  e.Before,
			After:   e.After,
			Created: hookutil.IsZeroSHA(e.Before),
			Deleted: hookutil.IsZeroSHA(e.After),
		}
		for _, c := range e.Commits {
			commit := forge.WebhookCommit{
				SHA:       c.ID,
				Message:   c.Message,
				URL:       c.URL,
				Timestamp: c.Timestamp,
			}
			if c.Author != nil {
				commit.AuthorName = c.Author.Name
				commit.AuthorEmail = c.Author.Email
			}
			push.Commits = append(push.Commits, commit)
		}
		event.Push = push
	case "pull_request":
		var e struct {
			PullRequest *gitea.PullRequest `json:"pull_request"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitea: decoding pull_request payload: %w", err)
		}
		if e.PullRequest != nil {
			pr := convertGiteaPR(e.PullRequest)
			event.PullRequest = &pr
		}
	case "issues":
		var e struct {
			Issue *gitea.Issue `json:"issue"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitea: decoding issues payload: %w", err)
		}
		if e.Issue != nil {
			issue := convertGiteaIssue(e.Issue)
			event.Issue = &issue
		}
	case "issue_comment":
		var e struct {
			Issue       *gitea.Issue       `json:"issue"`
			PullRequest *gitea.PullRequest `json:"pull_request"`
			Comment     *gitea.Comment     `json:"comment"`
			IsPull      bool               `json:"is_pull"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitea: decoding issue_comment payload: %w", err)
		}
		if e.Comment != nil {
			comment := convertGiteaComment(e.Comment)
			event.Comment = &comment
		}
		switch {
		case e.PullRequest != nil:
			pr := convertGiteaPR(e.PullRequest)
			event.PullRequest = &pr
		case e.Issue != nil && (e.IsPull || e.Issue.PullRequest != nil):
			pr := hookutil.IssueAsPullRequest(convertGiteaIssue(e.Issue))
			event.PullRequest = &pr
		case e.Issue != nil:
			issue := convertGiteaIssue(e.Issue)
			event.Issue = &issue
		}
	case "release":
		var e struct {
			Release *gitea.Release `json:"release"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitea: decoding release payload: %w", err)
		}
		if e.Release != nil {
			release := convertGiteaRelease(e.Release)
			event.Release = &release
		}
	}

	return event, nil
}

func convertGiteaHookUser(u *gitea.User) *forge.User {
	return &forge.User{
		Login:     u.UserName,
		Name:      u.FullName,
		AvatarURL: u.AvatarURL,
		HTMLURL:   u.HTMLURL,
	}
}
