package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/engine"
)

var (
	version = "dev"
	commit  = "none"
)

// tuiStopTimeout is how long the TUI has to exit after being asked to stop
// before it is killed.
const tuiStopTimeout = 5 * time.Second

func main() {
	rootCmd := &cobra.Command{
		Use:     "nami",
		Short:   "An agentic coding CLI powered by Go",
		Version: fmt.Sprintf("%s (%s)", version, commit),
		// Flags and arguments are checked by now, so an error from here on
		// is not a usage mistake and the usage text would only bury it.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cmd.SilenceUsage = true
		},
	}

	// Flags
	var (
		flagModel string
		flagMode  string
		flagStdio bool
		flagAuto  bool
	)
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", "", "Model to use (provider/model format, e.g. anthropic/claude-sonnet-5)")
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
	cfg = applyModelFlag(cfg, modelFlag)
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

// applyModelFlag applies --model the same way NAMI_MODEL is applied: a
// provider/model value sets both, and a bare model keeps the configured
// provider.
func applyModelFlag(cfg config.Config, modelFlag string) config.Config {
	modelFlag = strings.TrimSpace(modelFlag)
	if modelFlag == "" {
		return cfg
	}
	if provider, model := config.ParseModel(modelFlag); provider != "" {
		cfg.Provider = provider
		cfg.Model = model
	} else {
		cfg.Model = modelFlag
	}
	cfg.ModelSource = "flag"
	return cfg
}

// tuiModelSelection is the model handed to the TUI, which passes it back to
// the engine it starts as --model. Only a model chosen with --model or
// NAMI_MODEL is handed on: that engine reloads the config itself, and without
// one it prefers the model that last worked over the configured one. The
// provider travels with the model; a bare model would be paired with the
// configured provider rather than the one resolved here.
func tuiModelSelection(cfg config.Config) string {
	if cfg.ModelSource != "flag" && cfg.ModelSource != "env" {
		return ""
	}
	model := strings.TrimSpace(cfg.Model)
	provider := strings.TrimSpace(cfg.Provider)
	if model == "" || provider == "" {
		return model
	}
	return provider + "/" + model
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
	// When nami is told to stop, ask the TUI to stop too instead of killing
	// it, so it can put the terminal back and shut its engine down. Windows
	// has no SIGTERM, so there Cancel keeps its default, Kill.
	if runtime.GOOS != "windows" {
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	}
	cmd.WaitDelay = tuiStopTimeout
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"NAMI_ENGINE_PATH="+enginePath,
		"NAMI_MODE="+cfg.DefaultMode,
		"NAMI_AUTO_MODE="+strconv.FormatBool(cfg.AutoMode),
		"NAMI_COST_WARNING_THRESHOLD_USD="+strconv.FormatFloat(cfg.CostWarningThresholdUSD, 'f', -1, 64),
	)
	if model := tuiModelSelection(cfg); model != "" {
		cmd.Env = append(cmd.Env, "NAMI_MODEL="+model)
	}

	err = cmd.Run()
	if ctx.Err() != nil && stoppedAsAsked(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

// stoppedAsAsked reports whether the TUI ended the way it should once nami has
// passed on a request to stop: by exiting successfully (Run then reports the
// context's error) or by the SIGTERM itself. Being killed after the timeout
// is not.
func stoppedAsAsked(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return true
	}
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGTERM
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
