package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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

// mockCheckRunService implements forges.CheckRunService for testing.
type mockCheckRunService struct {
	listResult []forges.CheckRun
	listErr    error
	listSHA    string
	listCalls  int
}

func (m *mockCheckRunService) List(_ context.Context, _, _, sha string) ([]forges.CheckRun, error) {
	m.listCalls++
	m.listSHA = sha
	return m.listResult, m.listErr
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
			{State: forges.CommitStatusSuccess, Context: "ci/test", Description: "Tests passed"},
		},
	}
	checks := &mockCheckRunService{
		listResult: []forges.CheckRun{
			{ID: 11, Name: "lint", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess},
		},
	}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "abc123def456", Branch: "feature", Event: "push"},
			{ID: 2, Title: "stale build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "older-sha", Branch: "feature", Event: "push"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
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

	// Check runs are requested for the PR's head SHA.
	if checks.listCalls != 1 {
		t.Fatalf("CheckRuns.List calls = %d, want 1", checks.listCalls)
	}
	if checks.listSHA != "abc123def456" {
		t.Errorf("CheckRuns.List SHA = %q, want %q", checks.listSHA, "abc123def456")
	}

	// CI runs are requested for the head branch.
	if ci.listCalls != 1 {
		t.Fatalf("CI.ListRuns calls = %d, want 1", ci.listCalls)
	}
	if ci.listOpts.Branch != "feature" {
		t.Errorf("CI.ListRuns branch = %q, want %q", ci.listOpts.Branch, "feature")
	}

	for _, want := range []string{"Checks for #42", "Fix the thing", "ci/build", "ci/test", "success", "lint"} {
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

// TestPRChecksFailedExitCode exercises the command end to end: a failed
// commit status must produce exit code 1.
func TestPRChecksFailedExitCode(t *testing.T) {
	resetCmd(rootCmd)

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
			{State: forges.CommitStatusFailure, Context: "ci/test"},
		},
	}
	checks := &mockCheckRunService{}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "abc123"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err == nil {
		t.Fatalf("expected error for failed checks, got none; output:\n%s", out)
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.ExitCode() != prChecksExitFailed {
		t.Errorf("error = %v, want exit code %d", err, prChecksExitFailed)
	}
}

// TestPRChecksPendingExitCode exercises the command end to end: with no
// failures but a pending status, the exit code is 8.
func TestPRChecksPendingExitCode(t *testing.T) {
	resetCmd(rootCmd)

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
			{State: forges.CommitStatusPending, Context: "ci/test"},
		},
	}
	checks := &mockCheckRunService{}
	ci := &mockCIService{}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	_, err := runPRChecksCmd(t, []string{"42"})
	if err == nil {
		t.Fatal("expected error for pending checks, got none")
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.ExitCode() != prChecksExitPending {
		t.Errorf("error = %v, want exit code %d", err, prChecksExitPending)
	}
}

// TestPRChecksFailedCheckRunExitCode covers a check run failure that is
// invisible to the legacy status and workflow-run sources.
func TestPRChecksFailedCheckRunExitCode(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	statuses := &mockCommitStatusService{}
	checks := &mockCheckRunService{
		listResult: []forges.CheckRun{
			{ID: 7, Name: "third-party/scan", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionFailure},
		},
	}
	ci := &mockCIService{}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err == nil {
		t.Fatalf("expected error for failed check run, got none; output:\n%s", out)
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.ExitCode() != prChecksExitFailed {
		t.Errorf("error = %v, want exit code %d", err, prChecksExitFailed)
	}
	if !strings.Contains(out, "third-party/scan") {
		t.Errorf("output missing check run:\n%s", out)
	}
}

// TestPRChecksCheckRunsAbsentFromOtherSources covers check runs that appear
// in neither commit statuses nor CI runs, the gap the Checks API fills.
func TestPRChecksCheckRunsAbsentFromOtherSources(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	statuses := &mockCommitStatusService{listResult: []forges.CommitStatus{}}
	checks := &mockCheckRunService{
		listResult: []forges.CheckRun{
			{ID: 9, Name: "checks-only/sonar", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess},
			{ID: 10, Name: "checks-only/deploy", Status: forges.CIStatusRunning},
		},
	}
	ci := &mockCIService{listResult: []forges.CIRun{}}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	// The running deploy check is pending, so the command exits 8.
	var ee *exitError
	if !errors.As(err, &ee) || ee.ExitCode() != prChecksExitPending {
		t.Fatalf("error = %v, want exit code %d", err, prChecksExitPending)
	}
	for _, want := range []string{"Check runs:", "checks-only/sonar", "checks-only/deploy", "success", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestPRChecksPlainOutputConclusions covers plain output showing the
// conclusion for CI runs instead of a bare status.
func TestPRChecksPlainOutputConclusions(t *testing.T) {
	resetCmd(rootCmd)

	oldOutput := flagOutput
	flagOutput = "plain"
	defer func() { flagOutput = oldOutput }()

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	statuses := &mockCommitStatusService{}
	checks := &mockCheckRunService{}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess, SHA: "abc123"},
			{ID: 2, Title: "test", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionFailure, SHA: "abc123"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err == nil {
		t.Fatalf("expected error for failed CI run, got none; output:\n%s", out)
	}
	for _, want := range []string{"ci\t1\tbuild\tsuccess", "ci\t2\ttest\tfailure"} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing %q:\n%s", want, out)
		}
	}
	// Plain output is data lines only: the "Checks for #N" header is not
	// tab-separated and must not appear.
	if strings.Contains(out, "Checks for") {
		t.Errorf("plain output contains the header line:\n%s", out)
	}
	if strings.Contains(out, "ci\t1\tbuild\tcompleted") || strings.Contains(out, "ci\t2\ttest\tcompleted") {
		t.Errorf("plain output shows bare status instead of conclusion:\n%s", out)
	}
}

// TestPRChecksStalePendingStatusIgnored exercises the command end to end:
// ListStatuses returns the full history for a ref, so a context that posted
// pending and then success yields both entries. Only the newest status per
// context counts, so the command exits 0 and prints a single row.
func TestPRChecksStalePendingStatusIgnored(t *testing.T) {
	resetCmd(rootCmd)

	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	earlier := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	statuses := &mockCommitStatusService{
		listResult: []forges.CommitStatus{
			{State: forges.CommitStatusPending, Context: "ci/build", CreatedAt: earlier},
			{State: forges.CommitStatusSuccess, Context: "ci/build", CreatedAt: earlier.Add(time.Minute)},
		},
	}
	checks := &mockCheckRunService{}
	ci := &mockCIService{}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v (a stale pending status must not fail the command); output:\n%s", err, out)
	}
	if n := strings.Count(out, "ci/build"); n != 1 {
		t.Errorf("output has %d rows for ci/build, want 1:\n%s", n, out)
	}
	if strings.Contains(out, "pending") {
		t.Errorf("output shows the stale pending status:\n%s", out)
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
	checks := &mockCheckRunService{listErr: forges.ErrNotSupported}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
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
	checks := &mockCheckRunService{listErr: forges.ErrNotSupported}
	ci := &mockCIService{}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
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
	checks := &mockCheckRunService{
		listResult: []forges.CheckRun{
			{ID: 11, Name: "lint", Status: forges.CIStatusCompleted, Conclusion: forges.CIConclusionSuccess},
		},
	}
	ci := &mockCIService{
		listResult: []forges.CIRun{
			{ID: 1, Title: "build", Status: forges.CIStatusCompleted, SHA: "abc123"},
		},
	}
	resolve.SetTestForge(&mockForge{prService: prs, ciService: ci, commitStatusService: statuses, checkRunService: checks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v", err)
	}
	for _, want := range []string{`"commit_statuses"`, `"ci_runs"`, `"ci/build"`, `"check_runs"`, `"lint"`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestPRChecksEmptySourcesShown covers backends that return nil when a listed
// source has nothing: each section still prints and JSON has [] not null.
func TestPRChecksEmptySourcesShown(t *testing.T) {
	prs := &mockPRService{
		pr: &forges.PullRequest{
			Number: 42,
			Title:  "Fix the thing",
			Head:   forges.PRBranch{Ref: "feature", SHA: "abc123"},
		},
	}
	resolve.SetTestForge(&mockForge{
		prService:           prs,
		ciService:           &mockCIService{},
		commitStatusService: &mockCommitStatusService{},
		checkRunService:     &mockCheckRunService{},
	}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	resetCmd(rootCmd)
	out, err := runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks: %v", err)
	}
	for _, want := range []string{"No commit statuses reported", "No check runs reported", "No CI runs for this commit"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	oldOutput := flagOutput
	flagOutput = "json"
	defer func() { flagOutput = oldOutput }()

	out, err = runPRChecksCmd(t, []string{"42"})
	if err != nil {
		t.Fatalf("pr checks json: %v", err)
	}
	if strings.Contains(out, "null") {
		t.Errorf("JSON output has null for a listed source:\n%s", out)
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
