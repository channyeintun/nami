package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/channyeintun/go-cli/internal/config"
	"github.com/channyeintun/go-cli/internal/ipc"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "go-cli",
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

	// TUI mode: check for node, then spawn Ink frontend
	// (for now, fall back to stdio mode)
	fmt.Fprintln(os.Stderr, "TUI mode not yet implemented. Use --stdio for engine-only mode.")
	return runStdioEngine(ctx, cfg)
}

func runStdioEngine(ctx context.Context, cfg config.Config) error {
	bridge := ipc.NewBridge(os.Stdin, os.Stdout)

	// Emit ready event
	if err := bridge.EmitReady(); err != nil {
		return fmt.Errorf("emit ready: %w", err)
	}

	provider, model := config.ParseModel(cfg.Model)
	_ = provider
	_ = model

	// Main event loop: read client messages and dispatch
	for {
		msg, err := bridge.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("read message: %w", err)
		}

		switch msg.Type {
		case ipc.MsgShutdown:
			return nil
		case ipc.MsgCancel:
			// TODO: cancel in-flight query
			continue
		case ipc.MsgUserInput:
			// TODO: dispatch to query engine
			if err := bridge.Emit(ipc.EventTokenDelta, ipc.TokenDeltaPayload{
				Text: "Engine received your message. Query engine not yet implemented.\n",
			}); err != nil {
				return err
			}
			if err := bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{
				StopReason: "end_turn",
			}); err != nil {
				return err
			}
		case ipc.MsgSlashCommand:
			// TODO: dispatch slash commands
			continue
		case ipc.MsgModeToggle:
			// TODO: toggle mode
			continue
		case ipc.MsgPermissionResponse:
			// TODO: resolve pending permission request
			continue
		}
	}
}
