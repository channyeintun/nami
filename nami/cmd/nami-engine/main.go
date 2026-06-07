package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/engine"
	"github.com/channyeintun/nami/internal/tui"
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

	return tui.Run(ctx, cfg)
}
