package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	forge "github.com/git-pkgs/forge"
	"net/http"
	"strings"
	"time"

	"github.com/git-pkgs/forge/internal/hookutil"
)

// gitLabDeliveryEvents maps the X-Gitlab-Event header onto normalized names,
// matching the subscription names in gitLabWebhookEvents. Note hooks are
// resolved separately because their name depends on what was commented on.
var gitLabDeliveryEvents = map[string]string{
	"Push Hook":               "push",
	"Tag Push Hook":           "tag_push",
	"Issue Hook":              "issues",
	"Confidential Issue Hook": "issues",
	"Merge Request Hook":      "pull_request",
	"Release Hook":            "release",
	"Deployment Hook":         "deployment",
	"Wiki Page Hook":          "gollum",
	"Pipeline Hook":           "workflow_run",
	"Job Hook":                "workflow_job",
}

// gitLabActions maps GitLab issue, merge request, note and release actions
// onto GitHub's names. Actions without an equivalent, such as approved, are
// kept as sent.
var gitLabActions = map[string]string{
	"open":   "opened",
	"close":  "closed",
	"reopen": "reopened",
	"update": "edited",
	"merge":  "closed",
	"create": "created",
	"delete": "deleted",
}

type gitLabHookUser struct {
	Username  string `json:"username"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type gitLabHookProject struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
	GitHTTPURL        string `json:"git_http_url"`
	GitSSHURL         string `json:"git_ssh_url"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	AvatarURL         string `json:"avatar_url"`
	VisibilityLevel   *int   `json:"visibility_level"`
}

type gitLabHookLabel struct {
	Title       string `json:"title"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// gitLabHookObject covers the issue and merge request objects GitLab sends as
// object_attributes and inside note hooks.
type gitLabHookObject struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	Action       string `json:"action"`
	URL          string `json:"url"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Draft        bool   `json:"draft"`
	OldRev       string `json:"oldrev"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	ClosedAt     string `json:"closed_at"`
	LastCommit   struct {
		ID string `json:"id"`
	} `json:"last_commit"`
	Source *gitLabHookProject `json:"source"`
	Target *gitLabHookProject `json:"target"`
}

// ParseWebhook verifies and parses a GitLab webhook delivery. When secret is
// non-empty the X-Gitlab-Token header must equal it.
func ParseWebhook(r *http.Request, secret string) (*forge.WebhookEvent, error) {
	body, err := hookutil.ReadBody(r)
	if err != nil {
		return nil, fmt.Errorf("gitlab: %w", err)
	}

	if secret != "" && !hookutil.ValidToken(r.Header.Get("X-Gitlab-Token"), secret) {
		return nil, forge.ErrInvalidWebhookSignature
	}

	eventType := r.Header.Get("X-Gitlab-Event")
	if eventType == "" {
		return nil, errors.New("gitlab: webhook request has no X-Gitlab-Event header")
	}

	return parseGitLabEvent(eventType, r.Header.Get("X-Gitlab-Event-UUID"), body)
}

func parseGitLabEvent(eventType, deliveryID string, payload []byte) (*forge.WebhookEvent, error) {
	var envelope struct {
		User         *gitLabHookUser    `json:"user"`
		UserUsername string             `json:"user_username"`
		UserName     string             `json:"user_name"`
		UserAvatar   string             `json:"user_avatar"`
		Project      *gitLabHookProject `json:"project"`
		Action       string             `json:"action"`
		Attributes   struct {
			Action       string `json:"action"`
			NoteableType string `json:"noteable_type"`
		} `json:"object_attributes"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("gitlab: decoding webhook payload: %w", err)
	}

	event := &forge.WebhookEvent{
		Type:       eventType,
		DeliveryID: deliveryID,
		Payload:    json.RawMessage(payload),
	}
	if name, ok := gitLabDeliveryEvents[eventType]; ok {
		event.Type = name
	}
	if eventType == "Note Hook" || eventType == "Confidential Note Hook" {
		switch envelope.Attributes.NoteableType {
		case "Issue", "MergeRequest":
			event.Type = "issue_comment"
		case "Commit":
			event.Type = "commit_comment"
		}
	}

	action := envelope.Attributes.Action
	if action == "" {
		action = envelope.Action // release hooks put it at the top level
	}
	if mapped, ok := gitLabActions[action]; ok {
		action = mapped
	}
	event.Action = action

	if p := envelope.Project; p != nil && p.PathWithNamespace != "" {
		repo := convertGitLabHookProject(p)
		event.Repository = &repo
	}
	switch {
	case envelope.User != nil && envelope.User.Username != "":
		event.Sender = convertGitLabHookUser(envelope.User)
	case envelope.UserUsername != "":
		event.Sender = &forge.User{Login: envelope.UserUsername, Name: envelope.UserName, AvatarURL: envelope.UserAvatar}
	}

	switch event.Type {
	case "push", "tag_push":
		if err := parseGitLabPush(event, payload); err != nil {
			return nil, err
		}
	case "pull_request":
		var e struct {
			ObjectAttributes gitLabHookObject  `json:"object_attributes"`
			Labels           []gitLabHookLabel `json:"labels"`
			Assignees        []gitLabHookUser  `json:"assignees"`
			Reviewers        []gitLabHookUser  `json:"reviewers"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitlab: decoding merge request payload: %w", err)
		}
		pr := convertGitLabHookMR(&e.ObjectAttributes, e.Labels, e.Assignees, e.Reviewers)
		event.PullRequest = &pr
		if e.ObjectAttributes.Action == "update" && e.ObjectAttributes.OldRev != "" {
			event.Action = "synchronize"
		}
	case "issues":
		var e struct {
			ObjectAttributes gitLabHookObject  `json:"object_attributes"`
			Labels           []gitLabHookLabel `json:"labels"`
			Assignees        []gitLabHookUser  `json:"assignees"`
		}
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("gitlab: decoding issue payload: %w", err)
		}
		issue := convertGitLabHookIssue(&e.ObjectAttributes, e.Labels, e.Assignees)
		event.Issue = &issue
	case "issue_comment":
		if err := parseGitLabNote(event, payload); err != nil {
			return nil, err
		}
	case "release":
		if err := parseGitLabRelease(event, payload); err != nil {
			return nil, err
		}
	}

	return event, nil
}

func parseGitLabPush(event *forge.WebhookEvent, payload []byte) error {
	var e struct {
		Before  string `json:"before"`
		After   string `json:"after"`
		Ref     string `json:"ref"`
		Commits []struct {
			ID        string `json:"id"`
			Message   string `json:"message"`
			URL       string `json:"url"`
			Timestamp string `json:"timestamp"`
			Author    struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"author"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("gitlab: decoding push payload: %w", err)
	}
	push := &forge.WebhookPush{
		Ref:     e.Ref,
		Before:  e.Before,
		After:   e.After,
		Created: hookutil.IsZeroSHA(e.Before),
		Deleted: hookutil.IsZeroSHA(e.After),
	}
	for _, c := range e.Commits {
		push.Commits = append(push.Commits, forge.WebhookCommit{
			SHA:         c.ID,
			Message:     c.Message,
			URL:         c.URL,
			AuthorName:  c.Author.Name,
			AuthorEmail: c.Author.Email,
			Timestamp:   parseGitLabHookTime(c.Timestamp),
		})
	}
	event.Push = push
	return nil
}

func parseGitLabNote(event *forge.WebhookEvent, payload []byte) error {
	var e struct {
		User             *gitLabHookUser `json:"user"`
		ObjectAttributes struct {
			ID           int64  `json:"id"`
			Note         string `json:"note"`
			NoteableType string `json:"noteable_type"`
			URL          string `json:"url"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
		} `json:"object_attributes"`
		Issue        *gitLabHookObject `json:"issue"`
		MergeRequest *gitLabHookObject `json:"merge_request"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("gitlab: decoding note payload: %w", err)
	}

	attrs := e.ObjectAttributes
	comment := forge.Comment{
		ID:        attrs.ID,
		Body:      attrs.Note,
		HTMLURL:   attrs.URL,
		CreatedAt: parseGitLabHookTime(attrs.CreatedAt),
		UpdatedAt: parseGitLabHookTime(attrs.UpdatedAt),
	}
	if e.User != nil {
		comment.Author = *convertGitLabHookUser(e.User)
	}
	event.Comment = &comment
	if event.Action == "" {
		event.Action = "created"
	}

	switch {
	case attrs.NoteableType == "MergeRequest" && e.MergeRequest != nil:
		pr := convertGitLabHookMR(e.MergeRequest, nil, nil, nil)
		event.PullRequest = &pr
	case attrs.NoteableType == "Issue" && e.Issue != nil:
		issue := convertGitLabHookIssue(e.Issue, nil, nil)
		event.Issue = &issue
	}
	return nil
}

func parseGitLabRelease(event *forge.WebhookEvent, payload []byte) error {
	var e struct {
		Name        string `json:"name"`
		Tag         string `json:"tag"`
		Description string `json:"description"`
		URL         string `json:"url"`
		CreatedAt   string `json:"created_at"`
		ReleasedAt  string `json:"released_at"`
		Action      string `json:"action"`
		Commit      struct {
			ID string `json:"id"`
		} `json:"commit"`
		Assets struct {
			Links []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"links"`
			Sources []struct {
				Format string `json:"format"`
				URL    string `json:"url"`
			} `json:"sources"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(payload, &e); err != nil {
		return fmt.Errorf("gitlab: decoding release payload: %w", err)
	}

	release := forge.Release{
		TagName:     e.Tag,
		Title:       e.Name,
		Body:        e.Description,
		Target:      e.Commit.ID,
		HTMLURL:     e.URL,
		CreatedAt:   parseGitLabHookTime(e.CreatedAt),
		PublishedAt: parseGitLabHookTime(e.ReleasedAt),
	}
	for _, s := range e.Assets.Sources {
		switch s.Format {
		case "tar.gz":
			release.TarballURL = s.URL
		case "zip":
			release.ZipballURL = s.URL
		}
	}
	for _, l := range e.Assets.Links {
		release.Assets = append(release.Assets, forge.ReleaseAsset{
			ID:          l.ID,
			Name:        l.Name,
			DownloadURL: l.URL,
		})
	}
	event.Release = &release

	// GitLab releases are public as soon as they are created, which GitHub
	// reports as published.
	if e.Action == "create" {
		event.Action = "published"
	}
	return nil
}

func convertGitLabHookUser(u *gitLabHookUser) *forge.User {
	return &forge.User{
		Login:     u.Username,
		Name:      u.Name,
		AvatarURL: u.AvatarURL,
	}
}

// convertGitLabHookProject follows convertGitLabProject: Owner is the
// project's immediate namespace and Name is its display name.
func convertGitLabHookProject(p *gitLabHookProject) forge.Repository {
	repo := forge.Repository{
		FullName:      p.PathWithNamespace,
		Name:          p.Name,
		Description:   p.Description,
		HTMLURL:       p.WebURL,
		CloneURL:      p.GitHTTPURL,
		SSHURL:        p.GitSSHURL,
		DefaultBranch: p.DefaultBranch,
		LogoURL:       p.AvatarURL,
		Private:       p.VisibilityLevel != nil && *p.VisibilityLevel == 0,
	}
	parts := strings.Split(p.PathWithNamespace, "/")
	if len(parts) >= 2 {
		repo.Owner = parts[len(parts)-2]
	}
	return repo
}

func convertGitLabHookLabels(labels []gitLabHookLabel) []forge.Label {
	var result []forge.Label
	for _, l := range labels {
		result = append(result, forge.Label{Name: l.Title, Color: l.Color, Description: l.Description})
	}
	return result
}

func convertGitLabHookUsers(users []gitLabHookUser) []forge.User {
	var result []forge.User
	for i := range users {
		result = append(result, *convertGitLabHookUser(&users[i]))
	}
	return result
}

func convertGitLabHookMR(mr *gitLabHookObject, labels []gitLabHookLabel, assignees, reviewers []gitLabHookUser) forge.PullRequest {
	result := forge.PullRequest{
		Number:    mr.IID,
		Title:     mr.Title,
		Body:      mr.Description,
		State:     forge.NormalizePRStatus(mr.State),
		Draft:     mr.Draft,
		Head:      forge.PRBranch{Ref: mr.SourceBranch, SHA: mr.LastCommit.ID},
		Base:      forge.PRBranch{Ref: mr.TargetBranch},
		Merged:    mr.State == "merged",
		Labels:    convertGitLabHookLabels(labels),
		Assignees: convertGitLabHookUsers(assignees),
		Reviewers: convertGitLabHookUsers(reviewers),
		HTMLURL:   mr.URL,
		CreatedAt: parseGitLabHookTime(mr.CreatedAt),
		UpdatedAt: parseGitLabHookTime(mr.UpdatedAt),
	}
	if mr.Source != nil && mr.Target != nil && mr.Source.PathWithNamespace != mr.Target.PathWithNamespace {
		source := convertGitLabHookProject(mr.Source)
		result.Head.Fork = &forge.ForkInfo{
			Owner:    source.Owner,
			Name:     source.Name,
			CloneURL: source.CloneURL,
			SSHURL:   source.SSHURL,
		}
	}
	return result
}

func convertGitLabHookIssue(i *gitLabHookObject, labels []gitLabHookLabel, assignees []gitLabHookUser) forge.Issue {
	result := forge.Issue{
		Number:    i.IID,
		Title:     i.Title,
		Body:      i.Description,
		State:     forge.NormalizeIssueState(i.State),
		Labels:    convertGitLabHookLabels(labels),
		Assignees: convertGitLabHookUsers(assignees),
		HTMLURL:   i.URL,
		CreatedAt: parseGitLabHookTime(i.CreatedAt),
		UpdatedAt: parseGitLabHookTime(i.UpdatedAt),
	}
	if t := parseGitLabHookTime(i.ClosedAt); !t.IsZero() {
		result.ClosedAt = &t
	}
	return result
}

// gitLabHookTimeLayouts covers the timestamp formats GitLab uses in webhook
// payloads, which vary by event and GitLab version.
var gitLabHookTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05 MST",
	"2006-01-02 15:04:05 -0700",
}

func parseGitLabHookTime(s string) time.Time {
	for _, layout := range gitLabHookTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
