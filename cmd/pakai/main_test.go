package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dhanifudin/pakai/internal/config"
)

func TestVersionCommand(t *testing.T) {
	cmd := newVersionCmd()
	if cmd.Use != "version" {
		t.Errorf("version command Use = %q, want %q", cmd.Use, "version")
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.RunE(cmd, nil)
	if err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Error("version command produced no output")
	}
}

func TestVersionCommandRegistered(t *testing.T) {
	// Simulate what main() does: add version to root, then verify
	r := *rootCmd
	r.AddCommand(newVersionCmd())

	found := false
	for _, sub := range r.Commands() {
		if sub.Name() == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("root command should have a 'version' subcommand when added")
	}
}

func TestRootCommandStderr(t *testing.T) {
	if rootCmd.ErrOrStderr() != os.Stderr {
		t.Error("root command should write errors to stderr")
	}
}

func TestSetupTmuxCommandPrintsStatusSnippet(t *testing.T) {
	cmd := newSetupTmuxCmd()

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("setup tmux command returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "status-right") {
		t.Fatalf("expected tmux setup output to contain status-right, got %q", out)
	}
	if !strings.Contains(out, "status-interval 30") {
		t.Fatalf("expected tmux setup output to contain status-interval, got %q", out)
	}
}

// stubStdinWithDevNull replaces stdin with a non-terminal file, so prompting
// fails fast instead of blocking on a real TTY.
func stubStdinWithDevNull(t *testing.T) func() {
	t.Helper()
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = f
	return func() {
		os.Stdin = orig
		f.Close()
	}
}

func TestConfigSetSecretKeyMasksConfirmation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const key = "sk-airo-abcdefghijklmnopqrstuvwx"

	cmd := newConfigSetCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.RunE(cmd, []string{"provider.inferhub.key", key}); err != nil {
		t.Fatalf("set returned error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, key) {
		t.Fatalf("confirmation leaked the key: %q", out)
	}
	if !strings.Contains(out, "sk-air...uvwx") {
		t.Fatalf("confirmation = %q, want a masked key", out)
	}

	if got := config.GetProviderKey("inferhub"); got != key {
		t.Fatalf("stored key did not round-trip")
	}

	info, err := os.Stat(filepath.Join(dir, "pakai", "config.toml"))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config mode = %#o, want 0600", perm)
	}
}

func TestConfigSetSecretKeyWithoutValueNeedsTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	defer stubStdinWithDevNull(t)()

	cmd := newConfigSetCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.RunE(cmd, []string{"provider.inferhub.key"})
	if err == nil {
		t.Fatal("expected an error when prompting without a terminal")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("error = %v, want it to mention the terminal", err)
	}
}

func TestConfigSetNonSecretKeyWithoutValueFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cmd := newConfigSetCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.RunE(cmd, []string{"provider.inferhub.limit"})
	if err == nil {
		t.Fatal("expected an error for a missing value")
	}
	if !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("error = %v, want it to require a value", err)
	}
}

func TestConfigSetNonInteractiveStillWorks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	cmd := newConfigSetCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.RunE(cmd, []string{"provider.inferhub.limit", "20"}); err != nil {
		t.Fatalf("set returned error: %v", err)
	}
	if got := config.GetProviderLimit("inferhub"); got != 20 {
		t.Fatalf("limit = %v, want 20", got)
	}
}

func TestSetupHasInferhubSubcommand(t *testing.T) {
	for _, sub := range newSetupCmd().Commands() {
		if sub.Name() == "inferhub" {
			return
		}
	}
	t.Fatal("setup should have an 'inferhub' subcommand")
}
