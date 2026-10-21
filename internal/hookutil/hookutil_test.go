package hookutil

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	forges "github.com/git-pkgs/forge"
)

func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestValidHMACSHA256(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	sig := sign(payload, "s3cret")

	tests := []struct {
		name      string
		signature string
		want      bool
	}{
		{"bare hex", sig, true},
		{"sha256 prefix", "sha256=" + sig, true},
		{"wrong secret", sign(payload, "other"), false},
		{"not hex", "zz" + sig[2:], false},
		{"truncated", sig[:10], false},
		{"empty", "", false},
		{"sha1 prefix", "sha1=" + sig, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidHMACSHA256(payload, "s3cret", tt.signature); got != tt.want {
				t.Errorf("ValidHMACSHA256 = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidToken(t *testing.T) {
	if !ValidToken("s3cret", "s3cret") {
		t.Error("matching token rejected")
	}
	for _, token := range []string{"", "s3cre", "s3cret!", "S3CRET"} {
		if ValidToken(token, "s3cret") {
			t.Errorf("token %q accepted", token)
		}
	}
}

func TestIsZeroSHA(t *testing.T) {
	for sha, want := range map[string]bool{
		"0000000000000000000000000000000000000000":                         true,
		"0000000000000000000000000000000000000000000000000000000000000000": true,
		"": false,
		"a000000000000000000000000000000000000000": false,
	} {
		if got := IsZeroSHA(sha); got != want {
			t.Errorf("IsZeroSHA(%q) = %v, want %v", sha, got, want)
		}
	}
}

func TestPayload(t *testing.T) {
	payload := []byte(`{"ref":"refs/heads/main"}`)
	form := []byte("payload=" + url.QueryEscape(string(payload)))

	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        string
		wantErr     bool
	}{
		{"json", "application/json", payload, string(payload), false},
		{"json with charset", "application/json; charset=utf-8", payload, string(payload), false},
		{"missing content type", "", payload, string(payload), false},
		{"form", "application/x-www-form-urlencoded", form, string(payload), false},
		{"unsupported", "text/plain", payload, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Payload(tt.contentType, tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Payload = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("hello"))
	body, err := ReadBody(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q, want %q", body, "hello")
	}

	big := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(make([]byte, MaxBodyBytes+1)))
	if _, err := ReadBody(big); err == nil {
		t.Error("expected error for oversized body")
	}

	if _, err := ReadBody(&http.Request{}); err == nil {
		t.Error("expected error for missing body")
	}
}

func TestFirstHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/hook", nil)
	r.Header.Set("X-Second", "two")
	r.Header.Set("X-Third", "three")
	if got := FirstHeader(r, "X-First", "X-Second", "X-Third"); got != "two" {
		t.Errorf("FirstHeader = %q, want %q", got, "two")
	}
	if got := FirstHeader(r, "X-Missing"); got != "" {
		t.Errorf("FirstHeader = %q, want empty", got)
	}
}

func TestIssueAsPullRequest(t *testing.T) {
	for state, want := range map[forges.IssueState]forges.PRStatus{
		forges.IssueStateOpen:    forges.PRStatusOpen,
		forges.IssueStateClosed:  forges.PRStatusClosed,
		forges.IssueStateUnknown: forges.PRStatusUnknown,
	} {
		pr := IssueAsPullRequest(forges.Issue{Number: 7, Title: "Fix", State: state, HTMLURL: "https://example.com/pull/7"})
		if pr.State != want {
			t.Errorf("State for %q = %q, want %q", state, pr.State, want)
		}
		if pr.Number != 7 || pr.Title != "Fix" || pr.HTMLURL != "https://example.com/pull/7" {
			t.Errorf("fields not copied: %+v", pr)
		}
	}
}
