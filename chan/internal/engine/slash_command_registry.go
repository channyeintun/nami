package engine

import (
	commandspkg "github.com/channyeintun/chan/internal/commands"
	"github.com/channyeintun/chan/internal/ipc"
)

type slashCommandSpec struct {
	Descriptor commandspkg.Descriptor
	Handler    slashCommandHandler
}

func slashCommandSpecs() []slashCommandSpec {
	return []slashCommandSpec{
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "clear",
				Description:    "Clear the conversation and start a new session",
				Usage:          "/clear",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleClearSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "compact",
				Description:    "Compact the conversation to save context",
				Usage:          "/compact",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleCompactSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "connect",
				Description:    "Connect GitHub Copilot with device login",
				Usage:          "/connect [github-copilot [enterprise-domain]]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleConnectSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "debug",
				Description:    "Enable live debug logging and open the monitor popup",
				Usage:          "/debug [status|path|off]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleDebugSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "diff",
				Description:    "Show git diff (for example /diff --staged)",
				Usage:          "/diff [args]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleDiffSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "fast",
				Description:    "Switch to fast mode (direct execution)",
				Usage:          "/fast",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleFastSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "help",
				Description:    "Show the slash-command help text",
				Usage:          "/help",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleHelpSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "model",
				Description:    "Show the active model or open the model picker",
				Usage:          "/model [model]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleModelSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "plan",
				Description:    "Switch to plan mode (Ultrathink)",
				Usage:          "/plan",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handlePlanSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "plan-mode",
				Description:    "Alias for /plan",
				Usage:          "/plan-mode",
				TakesArguments: false,
				Hidden:         true,
			},
			Handler: slashCommandHandlerFunc(handlePlanSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "reasoning",
				Description:    "Show or set GPT-5 reasoning effort",
				Usage:          "/reasoning [low|medium|high|xhigh|default]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleReasoningSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "resume",
				Description:    "Resume a previous session",
				Usage:          "/resume [id]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleResumeSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "sessions",
				Description:    "List recent sessions",
				Usage:          "/sessions",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleSessionsSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "status",
				Description:    "Show the current session status",
				Usage:          "/status",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleStatusSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "subagent",
				Description:    "Show or switch the session subagent model",
				Usage:          "/subagent [model|default|help]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleSubagentSlashCommand),
		},
	}
}

func slashCommandCatalog() []commandspkg.Descriptor {
	catalog := make([]commandspkg.Descriptor, 0, len(slashCommandSpecs()))
	for _, spec := range slashCommandSpecs() {
		catalog = append(catalog, spec.Descriptor)
	}
	return catalog
}

func slashCommandDescriptors() []ipc.SlashCommandDescriptorPayload {
	return commandspkg.Descriptors(slashCommandCatalog())
}
