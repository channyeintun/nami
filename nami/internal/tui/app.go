package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/channyeintun/nami/internal/config"
)

func Run(ctx context.Context, cfg config.Config) error {
	program := tea.NewProgram(newModel(cfg), tea.WithContext(ctx))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
