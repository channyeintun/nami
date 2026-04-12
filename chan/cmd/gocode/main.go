package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/channyeintun/gocode/internal/config"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "gocode",
		Short:   "An agentic coding CLI powered by Go",
		Version: fmt.Sprintf("%s (%s)", version, commit),
	}

	// Flags
	var (
		flagModel string
		flagMode  string
		flagStdio bool
	)
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", "", "Model to use (provider/model format, e.g. anthropic/claude-sonnet-4-20250514)")
	rootCmd.PersistentFlags().StringVar(&flagMode, "mode", "", "Execution mode: plan or fast")
	rootCmd.PersistentFlags().BoolVar(&flagStdio, "stdio", false, "Run in stdio mode (NDJSON engine only, no TUI)")

	// Run command (default)
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the agent (default command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngine(flagModel, flagMode, flagStdio)
		},
	}
	rootCmd.AddCommand(runCmd)

	// Make "run" the default command
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runEngine(flagModel, flagMode, flagStdio)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runEngine(modelFlag, modeFlag string, stdioMode bool) error {
	cfg := config.Load()

	// CLI flag overrides
	if modelFlag != "" {
		cfg.Model = modelFlag
	}
	if modeFlag != "" {
		cfg.DefaultMode = modeFlag
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if stdioMode {
		return runStdioEngine(ctx, cfg)
	}

	return launchTUI(ctx, cfg)
}

func launchTUI(ctx context.Context, cfg config.Config) error {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("node is required for TUI mode: %w", err)
	}

	enginePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve engine executable: %w", err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(enginePath); resolveErr == nil {
		enginePath = resolvedPath
	}

	tuiEntry, err := resolveTUIEntry()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, nodePath, tuiEntry)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"GOCODE_ENGINE_PATH="+enginePath,
		"GOCODE_MODEL="+cfg.Model,
		"GOCODE_MODE="+cfg.DefaultMode,
		"GOCODE_COST_WARNING_THRESHOLD_USD="+strconv.FormatFloat(cfg.CostWarningThresholdUSD, 'f', -1, 64),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run ink tui: %w", err)
	}
	return nil
}

func resolveTUIEntry() (string, error) {
	if override := strings.TrimSpace(os.Getenv("GOCODE_TUI_ENTRY")); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("stat GOCODE_TUI_ENTRY: %w", err)
		}
		return override, nil
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve TUI entry: runtime caller unavailable")
	}

	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	tuiEntry := filepath.Join(moduleRoot, "tui", "dist", "index.js")
	if _, err := os.Stat(tuiEntry); err != nil {
		return "", fmt.Errorf("TUI bundle not found at %s: %w", tuiEntry, err)
	}
	return tuiEntry, nil
}
