package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/git-pkgs/forge"
	"github.com/git-pkgs/forge/internal/resolve"
	"github.com/spf13/cobra"
)

// mockCIService implements forges.CIService for testing.
type mockCIService struct {
	listResult []forges.CIRun
	listErr    error
	listOpts   forges.ListCIRunOpts
	listCalls  int
}

func (m *mockCIService) ListRuns(_ context.Context, _, _ string, opts forges.ListCIRunOpts) ([]forges.CIRun, error) {
	m.listCalls++
	m.listOpts = opts
	return m.listResult, m.listErr
}

func (m *mockCIService) GetRun(_ context.Context, _, _ string, _ int64) (*forges.CIRun, error) {
	return nil, nil
}

func (m *mockCIService) TriggerRun(_ context.Context, _, _ string, _ forges.TriggerCIRunOpts) error {
	return nil
}

func (m *mockCIService) CancelRun(_ context.Context, _, _ string, _ int64) error {
	return nil
}

func (m *mockCIService) RetryRun(_ context.Context, _, _ string, _ int64) error {
	return nil
}

func (m *mockCIService) GetJobLog(_ context.Context, _, _ string, _ int64) (io.ReadCloser, error) {
	return nil, nil
}

// mockCommitStatusService implements forges.CommitStatusService for testing.
type mockCommitStatusService struct {
	listResult []forges.CommitStatus
	listErr    error
	listSHA    string
	listCalls  int
}

func (m *mockCommitStatusService) List(_ context.Context, _, _, sha string) ([]forges.CommitStatus, error) {
	m.listCalls++
	m.listSHA = sha
	return m.listResult, m.listErr
}

func (m *mockCommitStatusService) Set(_ context.Context, _, _, _ string, _ forges.SetCommitStatusOpts) (*forges.CommitStatus, error) {
	return nil, nil
}

// runPRChecksCmd executes prChecksCmd with the given args, capturing stdout.
func runPRChecksCmd(t *testing.T, args []string) (string, error) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	cmd := prChecksCmd()
	cmd.SetArgs(args)
	runErr := cmd.Execute()

	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	_ = r.Close()
	return string(out), runErr
}

func TestPRChecksRegistered(t *testing.T) {
	found := false
	for _, sub := range prCmd.Commands() {
		if sub.Name() == "checks" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'checks' subcommand on pr command")
	}
}

func TestPRChecksInvalidNumber(t *testing.T) {
	resetCmd(rootCmd)
	_, err := runPRChecksCmd(t, []string{"notanumber"})
	if err == nil {
		t.Fatal("expected error for non-numeric PR number")
	}
	if !strings.Contains(err.Error(), "invalid PR number") {
		t.Errorf("expected 'invalid PR number' in error, got: %s", err)
	}
}

func TestPRChecksHappyPath(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123def456"},
		},
	}
	statuses := &mockCommitStatusService{
		listResult: []forges.CommitStatus{
			{State: forges.CommitStatusSuccess, Context: "ci/build", Description: "Build passed"},
			{State: forges.CommitStatusFailure, Context: "ci/test", Description: "Tests failed"},
		},
	}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "abc123def456", Branch: "feature", Event: "push"},
			{ID: 2, Title: "stale build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "older-sha", Branch: "feature", Event: "push"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v", err)
	}

	// Commit statuses are requested for the PR's head SHA.
	if statuses.listCalls != 1 {
		t.Fatalf("CommitStatuses.List calls = %d, want 1", statuses.listCalls)
	}
	if statuses.listSHA != "abc123def456" {
		t.Errorf("CommitStatuses.List SHA = %q, want %q", statuses.listSHA, "abc123def456")
	}

	// CI runs are requested for the head branch.
	if ci.listCalls != 1 {
		t.Fatalf("CI.ListRuns calls = %d, want 1", ci.listCalls)
	}
	if ci.listOpts.Branch != "feature" {
		t.Errorf("CI.ListRuns branch = %q, want %q", ci.listOpts.Branch, "feature")
	}

	for _, want := range []string{"Checks for #42", "Fix the thing", "ci/build", "ci/test", "success", "failure"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Only the run matching the head SHA is shown.
	if !strings.Contains(out, "1") || !strings.Contains(out, "build") {
		t.Errorf("output missing matching CI run:\n%s", out)
	}
	if strings.Contains(out, "stale build") {
		t.Errorf("output contains CI run for a different commit:\n%s", out)
	}
}

func TestPRChecksUnsupportedForge(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	ci := &mockCIService{listErr: forges.ErrNotSupported}
	statuses := &mockCommitStatusService{listErr: forges.ErrNotSupported}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	_, err := runPRChecksCmd(t, []string{"42"})
	if err == nil || !strings.Contains(err.Error(), "checks are not supported by this forge") {
		t.Fatalf("pr checks error = %v, want unsupported-forge error", err)
	}
}

func TestPRChecksSkipsCIRunsForFullRef(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "AGit PR",
			Head:   forges.PRBranch{Ref: "refs/pull/42/head", SHA: "abc123"},
		},
	}
	statuses := &mockCommitStatusService{
		listResult: []forges.CommitStatus{
			{State: forges.CommitStatusSuccess, Context: "ci/build"},
		},
	}
	ci := &mockCIService{}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v", err)
	}
	if ci.listCalls != 0 {
		t.Errorf("CI.ListRuns calls = %d, want 0 for a full-ref head", ci.listCalls)
	}
	if !strings.Contains(out, "ci/build") {
		t.Errorf("output missing commit statuses:\n%s", out)
	}
	if strings.Contains(out, "CI runs:") {
		t.Errorf("output should not contain a CI runs section for a full-ref head:\n%s", out)
	}
}

func TestPRChecksJSONOutput(t *testing.T) {
	resetCmd(rootCmd)

	oldOutput := flagOutput
	flagOutput = "json"
	defer func() { flagOutput = oldOutput }()

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	statuses := &mockCommitStatusService{
		listResult: []forges.CommitStatus{
			{State: forges.CommitStatusSuccess, Context: "ci/build"},
		},
	}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, SHA: "abc123"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v", err)
	}
	for _, want := range []string{`"commit_statuses"`, `"ci_runs"`, `"ci/build"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

func TestFilterCIRunsForSHA(t *testing.T) {
	runs := []forges.CIRun{
		{ID: 1, SHA: "abc"},
		{ID: 2, SHA: "def"},
		{ID: 3, SHA: ""},
	}

	filtered := filterCIRunsForSHA(runs, "abc")
	if len(filtered) != 2 || filtered[0].ID != 1 || filtered[1].ID != 3 {
		t.Errorf("filterCIRunsForSHA = %v, want runs 1 and 3", filtered)
	}

	if got := filterCIRunsForSHA(runs, ""); len(got) != 3 {
		t.Errorf("filterCIRunsForSHA with empty sha = %d runs, want 3", len(got))
	}

	var _ *cobra.Command
}
