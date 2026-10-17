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

// prChecksResult is the combined check status of a pull request.
type prChecksResult struct {
	PR             *forges.PullRequest   `json:"pr"`
	CommitStatuses []forges.CommitStatus `json:"commit_statuses"`
	CIRuns         []forges.CIRun        `json:"ci_runs"`
}

func prChecksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checks [<number>]",
		Short: "Show CI check status for a pull request",
		Long: `Show the check status for a pull request by number, or for the
pull request of the current branch when no number is given.

Lists commit statuses for the PR's head commit and CI pipeline runs
for the PR's head branch, like 'gh pr checks'.`,
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
			runsListed := false

			if pr.Head.SHA != "" {
				statuses, err := forge.CommitStatuses().List(cmd.Context(), owner, repoName, pr.Head.SHA)
				if err != nil {
					if !errors.Is(err, forges.ErrNotSupported) {
						return fmt.Errorf("listing commit statuses: %w", err)
					}
				} else {
					result.CommitStatuses = statuses
					statusesListed = true
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

			if !statusesListed && !runsListed {
				return fmt.Errorf("checks are not supported by this forge")
			}

			return printPRChecks(result)
		},
	}
	return cmd
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

func printPRChecks(result *prChecksResult) error {
	p := printer()
	if p.Format == output.JSON {
		return p.PrintJSON(result)
	}

	pr := result.PR
	sha := pr.Head.SHA
	if len(sha) > shortSHALength {
		sha = sha[:shortSHALength]
	}
	_, _ = fmt.Fprintf(os.Stdout, "Checks for #%d %s (%s on %s)\n",
		pr.Number, output.Sanitize(pr.Title), sha, pr.Head.Ref)

	if p.Format == output.Plain {
		var lines []string
		for _, s := range result.CommitStatuses {
			lines = append(lines, fmt.Sprintf("status\t%s\t%s", s.State, s.Context))
		}
		for _, r := range result.CIRuns {
			lines = append(lines, fmt.Sprintf("ci\t%d\t%s\t%s", r.ID, r.Title, r.Status))
		}
		p.PrintPlain(lines)
		return nil
	}

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

	if result.CIRuns != nil {
		_, _ = fmt.Fprintln(os.Stdout)
		_, _ = fmt.Fprintln(os.Stdout, "CI runs:")
		if len(result.CIRuns) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "  No CI runs for this commit")
		} else {
			headers := []string{"ID", "TITLE", "STATUS", "BRANCH", "EVENT", "CREATED"}
			rows := make([][]string, len(result.CIRuns))
			for i, r := range result.CIRuns {
				status := string(r.Status)
				if r.Conclusion != "" {
					status = string(r.Conclusion)
				}
				created := ""
				if !r.CreatedAt.IsZero() {
					created = r.CreatedAt.Format("2006-01-02 15:04")
				}
				rows[i] = []string{
					strconv.FormatInt(r.ID, 10),
					r.Title,
					status,
					r.Branch,
					r.Event,
					created,
				}
			}
			p.PrintTable(headers, rows)
		}
	}

	return nil
}
