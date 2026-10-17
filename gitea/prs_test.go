package gitea

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	forge "github.com/git-pkgs/forge"
)

// listPRsHeadPayload serves three open PRs: one from the same repo on
// branch "feature", one from a fork owned by "someone" on branch "feature",
// and one from the same repo on branch "other".
func servePRListHeadFixture(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	payload := []map[string]any{
		{
			"number": 1, "title": "same repo feature", "state": "open",
			"base": map[string]any{"ref": "main", "sha": "aaa", "repo_id": 100},
			"head": map[string]any{"ref": "feature", "sha": "bbb", "repo_id": 100},
		},
		{
			"number": 2, "title": "fork feature", "state": "open",
			"base": map[string]any{"ref": "main", "sha": "aaa", "repo_id": 100},
			"head": map[string]any{"ref": "feature", "sha": "ccc", "repo_id": 200,
				"repo": map[string]any{
					"owner": map[string]any{"login": "someone"},
					"name":  "hello-world",
				}},
		},
		{
			"number": 3, "title": "other branch", "state": "open",
			"base": map[string]any{"ref": "main", "sha": "aaa", "repo_id": 100},
			"head": map[string]any{"ref": "other", "sha": "ddd", "repo_id": 100},
		},
	}
	mux.HandleFunc("GET /api/v1/repos/octocat/hello-world/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(payload)
	})
}

func listPRNumbers(t *testing.T, f forge.Forge, head string) []int {
	t.Helper()
	prs, err := f.PullRequests().List(context.Background(), "octocat", "hello-world", forge.ListPROpts{Head: head})
	if err != nil {
		t.Fatalf("List with --head %q: unexpected error: %v", head, err)
	}
	nums := make([]int, len(prs))
	for i, pr := range prs {
		nums[i] = pr.Number
	}
	return nums
}

func assertPRNumbers(t *testing.T, head string, want, got []int) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("--head %q: want PRs %v, got %v", head, want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("--head %q: want PRs %v, got %v", head, want, got)
		}
	}
}

// https://github.com/git-pkgs/forge/issues/111
// The Gitea/Forgejo list-PR API has no server-side head filter, and the
// backend used to pass --head through silently unfiltered.
func TestGiteaListPRsAppliesHeadFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", giteaVersionHandler)
	servePRListHeadFixture(t, mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := New(srv.URL, "test-token", nil)

	assertPRNumbers(t, "feature", []int{1, 2}, listPRNumbers(t, f, "feature"))
	assertPRNumbers(t, "someone:feature", []int{2}, listPRNumbers(t, f, "someone:feature"))
	assertPRNumbers(t, "octocat:feature", []int{1}, listPRNumbers(t, f, "octocat:feature"))
	assertPRNumbers(t, "stranger:feature", []int{}, listPRNumbers(t, f, "stranger:feature"))
	assertPRNumbers(t, "other", []int{3}, listPRNumbers(t, f, "other"))
}
