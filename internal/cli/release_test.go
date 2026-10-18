package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/git-pkgs/forge"
	"github.com/git-pkgs/forge/internal/resolve"
)

const flagTypeBool = "bool"

func TestReleaseEditDraftFlag(t *testing.T) {
	cmd := releaseEditCmd()

	// The --draft flag should be a bool, not a string.
	f := cmd.Flags().Lookup("draft")
	if f == nil {
		t.Fatal("expected --draft flag to exist")
	}
	if f.Value.Type() != flagTypeBool {
		t.Errorf("expected --draft to be bool, got %s", f.Value.Type())
	}

	f = cmd.Flags().Lookup("prerelease")
	if f == nil {
		t.Fatal("expected --prerelease flag to exist")
	}
	if f.Value.Type() != flagTypeBool {
		t.Errorf("expected --prerelease to be bool, got %s", f.Value.Type())
	}
}

func TestReleaseCreateDraftFlag(t *testing.T) {
	cmd := releaseCreateCmd()

	f := cmd.Flags().Lookup("draft")
	if f == nil {
		t.Fatal("expected --draft flag to exist")
	}
	if f.Value.Type() != flagTypeBool {
		t.Errorf("expected --draft to be bool, got %s", f.Value.Type())
	}
}

// Ensure the cobra Command type is used (avoid import cycle).
var _ *cobra.Command

func TestReleaseSubcommands(t *testing.T) {
	subs := releaseCmd.Commands()
	want := map[string]bool{
		"list":     false,
		"view":     false,
		"create":   false,
		"edit":     false,
		"delete":   false,
		"upload":   false,
		"download": false,
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

// mockReleaseService implements forges.ReleaseService, recording UploadAsset calls.
type mockReleaseService struct {
	uploadCalls int
	uploadTag   string
	uploadBody  []byte
}

func (m *mockReleaseService) List(_ context.Context, _, _ string, _ forges.ListReleaseOpts) ([]forges.Release, error) {
	return nil, nil
}

func (m *mockReleaseService) Get(_ context.Context, _, _, _ string) (*forges.Release, error) {
	return nil, nil
}

func (m *mockReleaseService) GetLatest(_ context.Context, _, _ string) (*forges.Release, error) {
	return nil, nil
}

func (m *mockReleaseService) Create(_ context.Context, _, _ string, _ forges.CreateReleaseOpts) (*forges.Release, error) {
	return nil, nil
}

func (m *mockReleaseService) Update(_ context.Context, _, _, _ string, _ forges.UpdateReleaseOpts) (*forges.Release, error) {
	return nil, nil
}

func (m *mockReleaseService) Delete(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockReleaseService) UploadAsset(_ context.Context, _, _, tag string, file *os.File) (*forges.ReleaseAsset, error) {
	m.uploadCalls++
	m.uploadTag = tag
	body, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	m.uploadBody = body
	return &forges.ReleaseAsset{Name: filepath.Base(file.Name()), Size: len(body)}, nil
}

func (m *mockReleaseService) DownloadAsset(_ context.Context, _, _ string, _ int64) (io.ReadCloser, error) {
	return nil, nil
}

func TestReleaseUploadCommand(t *testing.T) {
	const contents = "fake-zip-bytes"

	tests := []struct {
		name    string
		args    []string
		wantTag string
		wantErr bool
	}{
		{name: "positional tag and file", args: []string{"v1.0.0", "{file}"}, wantTag: "v1.0.0"},
		{name: "tag flag and file", args: []string{"--tag", "v1.0.0", "{file}"}, wantTag: "v1.0.0"},
		{name: "tag flag with positional tag too", args: []string{"--tag", "v1.0.0", "v1.0.0", "{file}"}, wantErr: true},
		{name: "empty tag flag with positional tag", args: []string{"--tag", "", "v1.0.0", "{file}"}, wantErr: true},
		{name: "empty tag flag without positional tag", args: []string{"--tag", "", "{file}"}, wantErr: true},
		{name: "no tag at all", args: []string{"{file}"}, wantErr: true},
		{name: "tag flag without a file", args: []string{"--tag", "v1.0.0"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "app.zip")
			if err := os.WriteFile(file, []byte(contents), 0644); err != nil {
				t.Fatalf("writing test file: %v", err)
			}
			args := make([]string, len(tt.args))
			for i, a := range tt.args {
				if a == "{file}" {
					a = file
				}
				args[i] = a
			}

			releases := &mockReleaseService{}
			resolve.SetTestForge(&mockForge{releaseService: releases}, "owner", "repo", "example.com")
			t.Cleanup(resolve.ResetTestForge)

			cmd := releaseUploadCmd()
			cmd.SetArgs(args)
			err := cmd.Execute()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got success (tag %q uploaded)", releases.uploadTag)
				}
				if releases.uploadCalls != 0 {
					t.Errorf("UploadAsset calls = %d, want 0", releases.uploadCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if releases.uploadCalls != 1 {
				t.Fatalf("UploadAsset calls = %d, want 1", releases.uploadCalls)
			}
			if releases.uploadTag != tt.wantTag {
				t.Errorf("uploaded tag = %q, want %q", releases.uploadTag, tt.wantTag)
			}
			if string(releases.uploadBody) != contents {
				t.Errorf("uploaded body = %q, want %q", releases.uploadBody, contents)
			}
		})
	}
}

func TestUploadTagAndFile(t *testing.T) {
	tests := []struct {
		name       string
		flagTag    string
		tagChanged bool
		args       []string
		wantTag    string
		wantFile   string
		wantErr    bool
	}{
		{name: "positional tag and file", args: []string{"v1.0.0", "app.zip"}, wantTag: "v1.0.0", wantFile: "app.zip"},
		{name: "tag flag and file", flagTag: "v1.0.0", tagChanged: true, args: []string{"app.zip"}, wantTag: "v1.0.0", wantFile: "app.zip"},
		{name: "tag flag with positional tag too", flagTag: "v1.0.0", tagChanged: true, args: []string{"v1.0.0", "app.zip"}, wantErr: true},
		{name: "empty tag flag with positional tag", tagChanged: true, args: []string{"v1.0.0", "app.zip"}, wantErr: true},
		{name: "empty tag flag without positional tag", tagChanged: true, args: []string{"app.zip"}, wantErr: true},
		{name: "no tag at all", args: []string{"app.zip"}, wantErr: true},
		{name: "no arguments", wantErr: true},
		{name: "tag flag without a file", flagTag: "v1.0.0", tagChanged: true, wantErr: true},
		{name: "too many positional arguments", args: []string{"v1.0.0", "a.zip", "b.zip"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag, file, err := uploadTagAndFile(tt.flagTag, tt.tagChanged, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("uploadTagAndFile(%q, %v, %v) error = %v, wantErr %v", tt.flagTag, tt.tagChanged, tt.args, err, tt.wantErr)
			}
			if tag != tt.wantTag || file != tt.wantFile {
				t.Errorf("uploadTagAndFile(%q, %v, %v) = (%q, %q), want (%q, %q)",
					tt.flagTag, tt.tagChanged, tt.args, tag, file, tt.wantTag, tt.wantFile)
			}
		})
	}
}
