package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
)

func TestResolveTUIEntryPrefersExplicitOverride(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "custom-entry.mjs")
	if err := os.WriteFile(entry, []byte("// entry"), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	t.Setenv("NAMI_TUI_ENTRY", "  "+entry+"  ")

	got, err := resolveTUIEntry()
	if err != nil {
		t.Fatalf("resolveTUIEntry: %v", err)
	}
	if got != entry {
		t.Fatalf("entry = %q, want %q", got, entry)
	}
}

func TestResolveTUIEntryRejectsMissingOverride(t *testing.T) {
	t.Setenv("NAMI_TUI_ENTRY", filepath.Join(t.TempDir(), "missing.mjs"))
	if _, err := resolveTUIEntry(); err == nil {
		t.Fatal("resolveTUIEntry accepted an override that does not exist")
	}
}

// The bundler emits index.mjs; resolving index.js only would always fail.
func TestResolveTUIEntryFindsTheBuiltBundle(t *testing.T) {
	t.Setenv("NAMI_TUI_ENTRY", "")

	got, err := resolveTUIEntry()
	if err != nil {
		t.Skipf("TUI bundle is not built in this checkout: %v", err)
	}
	if base := filepath.Base(got); base != "index.mjs" && base != "index.js" {
		t.Fatalf("entry = %q, want the bundled entry point", got)
	}
	if _, statErr := os.Stat(got); statErr != nil {
		t.Fatalf("resolved entry does not exist: %v", statErr)
	}
}

// --model documents the provider/model form. Assigning it whole to the model
// paired the configured provider with a model id like "openai/gpt-5".
func TestApplyModelFlag(t *testing.T) {
	configured := config.Config{Provider: "anthropic", Model: "claude-sonnet-5", ModelSource: "config"}
	cases := []struct {
		flag         string
		wantProvider string
		wantModel    string
		wantSource   string
	}{
		{"openai/gpt-5", "openai", "gpt-5", "flag"},
		{" openai/gpt-5 ", "openai", "gpt-5", "flag"},
		{"groq/meta-llama/llama-4-scout", "groq", "meta-llama/llama-4-scout", "flag"},
		{"claude-opus-5", "anthropic", "claude-opus-5", "flag"},
		{"", "anthropic", "claude-sonnet-5", "config"},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			got := applyModelFlag(configured, tc.flag)
			if got.Provider != tc.wantProvider || got.Model != tc.wantModel || got.ModelSource != tc.wantSource {
				t.Fatalf("applyModelFlag(%q) = %q/%q (%s), want %q/%q (%s)",
					tc.flag, got.Provider, got.Model, got.ModelSource, tc.wantProvider, tc.wantModel, tc.wantSource)
			}
			provider, model := commandspkg.ResolveActiveSelection(got)
			if provider != tc.wantProvider || model != tc.wantModel {
				t.Fatalf("selection resolves to %q/%q, want %q/%q", provider, model, tc.wantProvider, tc.wantModel)
			}
		})
	}
}

// The TUI hands NAMI_MODEL back to the engine it starts as --model, and that
// engine reloads the config. A bare model there would be paired with the
// configured provider instead of the one resolved here.
func TestTUIModelSelectionSurvivesTheRoundTrip(t *testing.T) {
	configured := config.Config{Provider: "anthropic", Model: "claude-sonnet-5", ModelSource: "config"}
	chosen := []config.Config{
		{Provider: "openai", Model: "gpt-5"},
		{Provider: "groq", Model: "meta-llama/llama-4-scout"},
		{Provider: "anthropic", Model: "claude-sonnet-5"},
	}
	for _, parent := range chosen {
		t.Run(parent.Provider+"/"+parent.Model, func(t *testing.T) {
			child := applyModelFlag(configured, tuiModelSelection(parent))
			if child.Provider != parent.Provider || child.Model != parent.Model {
				t.Fatalf("engine started by the TUI resolves %q/%q, want %q/%q", child.Provider, child.Model, parent.Provider, parent.Model)
			}
		})
	}

	if got := tuiModelSelection(config.Config{Model: "gpt-5"}); got != "gpt-5" {
		t.Errorf("tuiModelSelection without a provider = %q, want the bare model", got)
	}
	if got := tuiModelSelection(config.Config{Provider: "anthropic"}); got != "" {
		t.Errorf("tuiModelSelection without a model = %q, want empty so the engine picks the default", got)
	}
}

func TestRenderMCPResultSplitsStreams(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := renderMCPResult(cmd, commandspkg.MCPCommandResult{
		OutputLines:  []string{"line one", "line two"},
		WarningLines: []string{"careful"},
	})
	if err != nil {
		t.Fatalf("renderMCPResult: %v", err)
	}

	if got := stdout.String(); got != "line one\nline two\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "careful\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestRenderMCPResultWithNoLines(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := renderMCPResult(cmd, commandspkg.MCPCommandResult{}); err != nil {
		t.Fatalf("renderMCPResult: %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, want both empty", stdout.String(), stderr.String())
	}
}

func TestMCPCommandTree(t *testing.T) {
	root := newMCPCommand()
	if root.Use != "mcp" {
		t.Fatalf("Use = %q", root.Use)
	}

	want := map[string]bool{"add": false, "add-json": false, "list": false, "get": false, "remove": false}
	for _, sub := range root.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("mcp command is missing the %q subcommand", name)
		}
	}
}

func TestDebugAndTimingCommandsAreWired(t *testing.T) {
	debug := newDebugViewCommand()
	if strings.Fields(debug.Use)[0] != "debug-view" {
		t.Fatalf("debug command Use = %q", debug.Use)
	}
	if debug.RunE == nil {
		t.Error("debug-view has no run function")
	}

	timing := newTimingSummaryCommand()
	if timing.RunE == nil {
		t.Error("timing summary has no run function")
	}
}

func TestMCPAddCommandRequiresArguments(t *testing.T) {
	cmd := newMCPAddCommand()
	if cmd.Args == nil {
		t.Fatal("mcp add does not validate its arguments")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("mcp add accepted zero arguments")
	}
	if err := cmd.Args(cmd, []string{"name", "command"}); err != nil {
		t.Errorf("mcp add rejected a valid argument list: %v", err)
	}
}
