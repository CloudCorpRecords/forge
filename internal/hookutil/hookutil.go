// Package hookutil holds helpers shared by the backends' webhook parsers.
package hookutil

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	forges "github.com/git-pkgs/forge"
)

// MaxBodyBytes caps how much of a delivery is read. GitHub caps webhook
// payloads at 25 MB and the other forges send less.
const MaxBodyBytes = 25 << 20

// ReadBody reads a delivery body, failing once it passes MaxBodyBytes.
func ReadBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("webhook request has no body")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading webhook body: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, fmt.Errorf("webhook body exceeds %d bytes", MaxBodyBytes)
	}
	return body, nil
}

// Payload returns the JSON payload of a delivery. Form deliveries carry it in
// the payload field; JSON deliveries are the payload. A missing Content-Type
// is treated as JSON.
func Payload(contentType string, body []byte) ([]byte, error) {
	if contentType == "" {
		return body, nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("parsing webhook content type: %w", err)
	}
	switch mediaType {
	case "application/json":
		return body, nil
	case "application/x-www-form-urlencoded":
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("parsing webhook form body: %w", err)
		}
		return []byte(form.Get("payload")), nil
	default:
		return nil, fmt.Errorf("unsupported webhook content type %q", mediaType)
	}
}

// ValidHMACSHA256 reports whether signature is the hex HMAC-SHA256 of payload
// keyed with secret. A "sha256=" prefix is accepted.
func ValidHMACSHA256(payload []byte, secret, signature string) bool {
	want, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(want) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hmac.Equal(mac.Sum(nil), want)
}

// ValidToken reports whether a shared-secret token matches, in constant time.
func ValidToken(token, secret string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

// IsZeroSHA reports whether sha is a non-empty run of zeros, which forges send
// as the before or after commit of a push that creates or deletes a ref.
func IsZeroSHA(sha string) bool {
	return sha != "" && strings.Trim(sha, "0") == ""
}

// FirstHeader returns the first non-empty value among the named headers.
func FirstHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if v := r.Header.Get(name); v != "" {
			return v
		}
	}
	return ""
}

// IssueAsPullRequest builds a pull request from the issue representation that
// GitHub and Gitea send for comments on pull requests.
func IssueAsPullRequest(i forges.Issue) forges.PullRequest {
	state := forges.PRStatusUnknown
	switch i.State {
	case forges.IssueStateOpen:
		state = forges.PRStatusOpen
	case forges.IssueStateClosed:
		state = forges.PRStatusClosed
	}
	return forges.PullRequest{
		Number:    i.Number,
		Title:     i.Title,
		Body:      i.Body,
		State:     state,
		Author:    i.Author,
		Assignees: i.Assignees,
		Labels:    i.Labels,
		Milestone: i.Milestone,
		Comments:  i.Comments,
		HTMLURL:   i.HTMLURL,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
		ClosedAt:  i.ClosedAt,
	}
}
