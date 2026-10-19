package cli

import (
	"context"
	"io"
	"testing"

	"github.com/git-pkgs/forge"
	"github.com/git-pkgs/forge/internal/resolve"
)

type mockWebhookService struct {
	deletedIDs []int64
}

func (m *mockWebhookService) List(context.Context, string, string, forges.ListWebhookOpts) ([]forges.Webhook, error) {
	return nil, nil
}

func (m *mockWebhookService) Get(context.Context, string, string, int64) (*forges.Webhook, error) {
	return nil, nil
}

func (m *mockWebhookService) Create(context.Context, string, string, forges.CreateWebhookOpts) (*forges.Webhook, error) {
	return nil, nil
}

func (m *mockWebhookService) Delete(_ context.Context, _, _ string, id int64) error {
	m.deletedIDs = append(m.deletedIDs, id)
	return nil
}

func TestWebhookSubcommands(t *testing.T) {
	subs := webhookCmd.Commands()
	want := map[string]bool{
		"list":   false,
		"add":    false,
		"delete": false,
	}

	for _, cmd := range subs {
		if _, ok := want[cmd.Name()]; ok {
			want[cmd.Name()] = true
		}
	}

	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand: %s", name)
		}
	}
}

func TestWebhookDeleteRejectsInvalidID(t *testing.T) {
	for _, arg := range []string{"7oops", "abc", "", "7.5", "0", "-1", "99999999999999999999"} {
		t.Run(arg, func(t *testing.T) {
			hooks := &mockWebhookService{}
			resolve.SetTestForge(&mockForge{webhookService: hooks}, "owner", "repo", "example.com")
			t.Cleanup(resolve.ResetTestForge)

			cmd := webhookDeleteCmd()
			cmd.SetArgs([]string{arg, "--yes"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("webhook delete %q: expected error, got nil", arg)
			}
			if len(hooks.deletedIDs) != 0 {
				t.Fatalf("webhook delete %q sent delete requests for %v", arg, hooks.deletedIDs)
			}
		})
	}
}

func TestWebhookDeleteValidID(t *testing.T) {
	hooks := &mockWebhookService{}
	resolve.SetTestForge(&mockForge{webhookService: hooks}, "owner", "repo", "example.com")
	t.Cleanup(resolve.ResetTestForge)

	cmd := webhookDeleteCmd()
	cmd.SetArgs([]string{"7", "--yes"})
	stdout, err := captureStdout(t, cmd.Execute)
	if err != nil {
		t.Fatalf("webhook delete: %v", err)
	}
	if len(hooks.deletedIDs) != 1 || hooks.deletedIDs[0] != 7 {
		t.Fatalf("deleted IDs = %v, want [7]", hooks.deletedIDs)
	}
	if stdout != "Deleted webhook 7\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "Deleted webhook 7\n")
	}
}
