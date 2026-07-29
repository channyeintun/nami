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

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/engine"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "nami",
		Short:   "An agentic coding CLI powered by Go",
		Version: fmt.Sprintf("%s (%s)", version, commit),
	}

	// Flags
	var (
		flagModel string
		flagMode  string
		flagStdio bool
		flagAuto  bool
	)
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", "", "Model to use (provider/model format, e.g. github-copilot/gpt-5.4)")
	rootCmd.PersistentFlags().StringVar(&flagMode, "mode", "", "Execution mode: plan or fast")
	rootCmd.PersistentFlags().BoolVar(&flagStdio, "stdio", false, "Run in stdio mode (NDJSON engine only, no TUI)")
	rootCmd.PersistentFlags().BoolVar(&flagAuto, "auto-mode", false, "Auto-approve non-destructive tool calls")

	// Run command (default)
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the agent (default command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngine(flagModel, flagMode, flagStdio, flagAuto)
		},
	}
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(newDebugViewCommand())
	rootCmd.AddCommand(newMCPCommand())
	rootCmd.AddCommand(newTimingSummaryCommand())

	// Make "run" the default command
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runEngine(flagModel, flagMode, flagStdio, flagAuto)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runEngine(modelFlag, modeFlag string, stdioMode, autoMode bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg := config.LoadForWorkingDir(cwd)

	// CLI flag overrides
	if modelFlag != "" {
		cfg.Model = modelFlag
		cfg.ModelSource = "flag"
	}
	if modeFlag != "" {
		cfg.DefaultMode = modeFlag
	}
	if autoMode {
		cfg.AutoMode = true
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
		return engine.RunStdioEngine(ctx, cfg)
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
		"NAMI_ENGINE_PATH="+enginePath,
		"NAMI_MODEL="+cfg.Model,
		"NAMI_MODE="+cfg.DefaultMode,
		"NAMI_AUTO_MODE="+strconv.FormatBool(cfg.AutoMode),
		"NAMI_COST_WARNING_THRESHOLD_USD="+strconv.FormatFloat(cfg.CostWarningThresholdUSD, 'f', -1, 64),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run ink tui: %w", err)
	}
	return nil
}

func resolveTUIEntry() (string, error) {
	if override := strings.TrimSpace(os.Getenv("NAMI_TUI_ENTRY")); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("stat NAMI_TUI_ENTRY: %w", err)
		}
		return override, nil
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve TUI entry: runtime caller unavailable")
	}

	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	distDir := filepath.Join(moduleRoot, "tui", "dist")
	// The bundler emits an ES module; the .js name is kept as a fallback for
	// bundles produced before that switch.
	for _, name := range []string{"index.mjs", "index.js"} {
		candidate := filepath.Join(distDir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("TUI bundle not found in %s: run make -C tui release-local, or set NAMI_TUI_ENTRY", distDir)
}
