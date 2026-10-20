package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/git-pkgs/forge"
	"github.com/git-pkgs/forge/internal/output"
	"github.com/git-pkgs/forge/internal/resolve"
	"github.com/spf13/cobra"
)

// defaultPRChecksRunLimit bounds how many CI runs are fetched for the head
// branch before narrowing them down to runs for the PR's head commit.
const defaultPRChecksRunLimit = 20

// Exit codes for pr checks. A failed check is more decisive than a pending
// one, so failures take precedence when both are present.
const (
	prChecksExitFailed  = 1
	prChecksExitPending = 8 // matches 'gh pr checks'
)

// exitError is a command error that carries a process exit code. cmd/forge
// maps it to the real process exit status.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// prChecksResult is the combined check status of a pull request.
type prChecksResult struct {
	PR             *forges.PullRequest   `json:"pr"`
	CommitStatuses []forges.CommitStatus `json:"commit_statuses"`
	CheckRuns      []forges.CheckRun     `json:"check_runs"`
	CIRuns         []forges.CIRun        `json:"ci_runs"`
}

func prChecksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checks [<number>]",
		Short: "Show CI check status for a pull request",
		Long: `Show the check status for a pull request by number, or for the
pull request of the current branch when no number is given.

Lists commit statuses and check runs for the PR's head commit and CI
pipeline runs for the PR's head branch, like 'gh pr checks'.

Exits 0 when every check passed, 1 when any check failed, and 8 when
no check failed but some are still pending.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			forge, owner, repoName, _, err := resolve.Repo(flagRepo, flagForgeType)
			if err != nil {
				return err
			}

			var number int
			if len(args) > 0 {
				number, err = strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid PR number: %s", args[0])
				}
			} else {
				number, err = findPRForCurrentBranch(cmd.Context(), forge, owner, repoName)
				if err != nil {
					return err
				}
			}

			pr, err := forge.PullRequests().Get(cmd.Context(), owner, repoName, number)
			if err != nil {
				return fmt.Errorf("getting PR #%d: %w", number, err)
			}

			result := &prChecksResult{PR: pr}
			statusesListed := false
			checksListed := false
			runsListed := false

			if pr.Head.SHA != "" {
				statuses, err := forge.CommitStatuses().List(cmd.Context(), owner, repoName, pr.Head.SHA)
				if err != nil {
					if !errors.Is(err, forges.ErrNotSupported) {
						return fmt.Errorf("listing commit statuses: %w", err)
					}
				} else {
					result.CommitStatuses = latestCommitStatuses(statuses)
					statusesListed = true
				}

				// Check runs (the Checks API) cover third-party checks and
				// individual Actions jobs that commit statuses and workflow
				// runs omit.
				checkRuns, err := forge.CheckRuns().List(cmd.Context(), owner, repoName, pr.Head.SHA)
				if err != nil {
					if !errors.Is(err, forges.ErrNotSupported) {
						return fmt.Errorf("listing check runs: %w", err)
					}
				} else {
					result.CheckRuns = checkRuns
					checksListed = true
				}
			}

			// A full ref (e.g. refs/pull/<n>/head) is not a branch on the
			// remote, so there are no branch CI runs to list for it.
			if pr.Head.Ref != "" && !isFullRef(pr.Head.Ref) {
				runs, err := forge.CI().ListRuns(cmd.Context(), owner, repoName, forges.ListCIRunOpts{
					Branch: pr.Head.Ref,
					Limit:  defaultPRChecksRunLimit,
				})
				if err != nil {
					if !errors.Is(err, forges.ErrNotSupported) {
						return fmt.Errorf("listing CI runs: %w", err)
					}
				} else {
					result.CIRuns = filterCIRunsForSHA(runs, pr.Head.SHA)
					runsListed = true
				}
			}

			if !statusesListed && !checksListed && !runsListed {
				return fmt.Errorf("checks are not supported by this forge")
			}

			return printPRChecks(result)
		},
	}
	return cmd
}

// latestCommitStatuses collapses the full status history that ListStatuses
// returns for a ref to the newest entry per Context (by CreatedAt), keeping
// first-seen context order. Without this, a context that posted pending and
// then success keeps both entries: the table shows duplicate rows and the
// stale pending makes the command exit nonzero forever.
func latestCommitStatuses(statuses []forges.CommitStatus) []forges.CommitStatus {
	latest := make(map[string]int, len(statuses))
	var order []string
	for i, s := range statuses {
		j, seen := latest[s.Context]
		if !seen {
			latest[s.Context] = i
			order = append(order, s.Context)
			continue
		}
		if s.CreatedAt.After(statuses[j].CreatedAt) {
			latest[s.Context] = i
		}
	}
	out := make([]forges.CommitStatus, 0, len(order))
	for _, context := range order {
		out = append(out, statuses[latest[context]])
	}
	return out
}

// filterCIRunsForSHA keeps only runs for the given head commit. Runs that do
// not report a commit SHA are kept, since they cannot be filtered precisely.
func filterCIRunsForSHA(runs []forges.CIRun, sha string) []forges.CIRun {
	if sha == "" {
		return runs
	}
	filtered := runs[:0]
	for _, r := range runs {
		if r.SHA == "" || r.SHA == sha {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// checkRunOutcome classifies a run's status/conclusion pair as failed or
// pending. A decisive conclusion wins; otherwise the status decides.
func checkRunOutcome(status forges.CIStatus, conclusion forges.CIConclusion) (failed, pending bool) {
	switch conclusion {
	case forges.CIConclusionFailure, forges.CIConclusionCancelled,
		forges.CIConclusionTimedOut, forges.CIConclusionActionRequired:
		return true, false
	case forges.CIConclusionSuccess, forges.CIConclusionSkipped,
		forges.CIConclusionNeutral:
		return false, false
	}
	switch status {
	case forges.CIStatusFailed:
		return true, false
	case forges.CIStatusQueued, forges.CIStatusRunning:
		return false, true
	}
	return false, false
}

// prChecksExitError returns a nonzero exit error when any check failed or is
// still pending, nil when everything passed.
func prChecksExitError(result *prChecksResult) error {
	failed, pending := false, false
	for _, s := range result.CommitStatuses {
		switch s.State {
		case forges.CommitStatusFailure, forges.CommitStatusError:
			failed = true
		case forges.CommitStatusPending:
			pending = true
		}
	}
	for _, c := range result.CheckRuns {
		f, p := checkRunOutcome(c.Status, c.Conclusion)
		failed, pending = failed || f, pending || p
	}
	for _, r := range result.CIRuns {
		f, p := checkRunOutcome(r.Status, r.Conclusion)
		failed, pending = failed || f, pending || p
	}
	switch {
	case failed:
		return &exitError{code: prChecksExitFailed, msg: "some checks failed"}
	case pending:
		return &exitError{code: prChecksExitPending, msg: "some checks are pending"}
	default:
		return nil
	}
}

// runDisplayStatus prefers the conclusion when the run reports one, so
// completed runs show their result instead of a bare "completed".
func runDisplayStatus(status forges.CIStatus, conclusion forges.CIConclusion) string {
	if conclusion != "" {
		return string(conclusion)
	}
	return string(status)
}

func printPRChecks(result *prChecksResult) error {
	p := printer()
	if p.Format == output.JSON {
		if err := p.PrintJSON(result); err != nil {
			return err
		}
		return prChecksExitError(result)
	}

	if p.Format == output.Plain {
		var lines []string
		for _, s := range result.CommitStatuses {
			lines = append(lines, fmt.Sprintf("status\t%s\t%s", s.State, s.Context))
		}
		for _, c := range result.CheckRuns {
			lines = append(lines, fmt.Sprintf("check\t%s\t%s", c.Name, runDisplayStatus(c.Status, c.Conclusion)))
		}
		for _, r := range result.CIRuns {
			lines = append(lines, fmt.Sprintf("ci\t%d\t%s\t%s", r.ID, r.Title, runDisplayStatus(r.Status, r.Conclusion)))
		}
		p.PrintPlain(lines)
		return prChecksExitError(result)
	}

	// The header is not a data line, so it is printed only after the plain
	// branch above, like the other commands' plain output.
	pr := result.PR
	sha := pr.Head.SHA
	if len(sha) > shortSHALength {
		sha = sha[:shortSHALength]
	}
	_, _ = fmt.Fprintf(os.Stdout, "Checks for #%d %s (%s on %s)\n",
		pr.Number, output.Sanitize(pr.Title), sha, pr.Head.Ref)

	if result.CommitStatuses != nil {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Commit statuses:")
		if len(result.CommitStatuses) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "  No commit statuses reported")
		} else {
			headers := []string{"STATE", "CONTEXT", "DESCRIPTION", "URL"}
			rows := make([][]string, len(result.CommitStatuses))
			for i, s := range result.CommitStatuses {
				rows[i] = []string{
					string(s.State),
					s.Context,
					s.Description,
					s.TargetURL,
				}
			}
			p.PrintTable(headers, rows)
		}
	}

	if result.CheckRuns != nil {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "Check runs:")
		if len(result.CheckRuns) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "  No check runs reported")
		} else {
			headers := []string{"ID", "NAME", "STATUS", "CONCLUSION", "URL"}
			rows := make([][]string, len(result.CheckRuns))
			for i, c := range result.CheckRuns {
				rows[i] = []string{
					strconv.FormatInt(c.ID, 10),
					c.Name,
					string(c.Status),
					string(c.Conclusion),
					c.HTMLURL,
				}
			}
			p.PrintTable(headers, rows)
		}
	}

	if result.CIRuns != nil {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "CI runs:")
		if len(result.CIRuns) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "  No CI runs for this commit")
		} else {
			headers := []string{"ID", "TITLE", "STATUS", "BRANCH", "EVENT", "CREATED"}
			rows := make([][]string, len(result.CIRuns))
			for i, r := range result.CIRuns {
				created := ""
				if !r.CreatedAt.IsZero() {
					created = r.CreatedAt.Format("2006-01-02 15:04")
				}
				rows[i] = []string{
					strconv.FormatInt(r.ID, 10),
					r.Title,
					runDisplayStatus(r.Status, r.Conclusion),
					r.Branch,
					r.Event,
					created,
				}
			}
			p.PrintTable(headers, rows)
		}
	}

	return prChecksExitError(result)
}
