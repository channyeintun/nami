package engine

import (
	"sort"
	"strings"

	commandspkg "github.com/channyeintun/chan/internal/commands"
	"github.com/channyeintun/chan/internal/ipc"
	skillspkg "github.com/channyeintun/chan/internal/skills"
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
				Description:    "Connect and set up model providers",
				Usage:          "/connect [provider|status|help]",
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
				Name:           "providers",
				Description:    "Show provider availability and setup state",
				Usage:          "/providers",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleProvidersSlashCommand),
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
				Description:    "Show or set reasoning effort [low|medium|high|xhigh|default]",
				Usage:          "/reasoning [low|medium|high|xhigh|default]",
				TakesArguments: true,
			},
			Handler: slashCommandHandlerFunc(handleReasoningSlashCommand),
		},
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "rewind",
				Description:    "Jump back to an earlier user turn and drop later context",
				Usage:          "/rewind",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleRewindSlashCommand),
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
		{
			Descriptor: commandspkg.Descriptor{
				Name:           "tasks",
				Description:    "Open the background tasks dialog",
				Usage:          "/tasks",
				TakesArguments: false,
			},
			Handler: slashCommandHandlerFunc(handleTasksSlashCommand),
		},
	}
}

func builtinSlashCommandCatalog() []commandspkg.Descriptor {
	catalog := make([]commandspkg.Descriptor, 0, len(slashCommandSpecs()))
	for _, spec := range slashCommandSpecs() {
		catalog = append(catalog, spec.Descriptor)
	}
	return catalog
}

func builtinSlashCommandNames() map[string]struct{} {
	names := make(map[string]struct{}, len(slashCommandSpecs()))
	for _, spec := range slashCommandSpecs() {
		names[strings.ToLower(strings.TrimSpace(spec.Descriptor.Name))] = struct{}{}
	}
	return names
}

func skillSlashCommandCatalog(skills []skillspkg.Skill) []commandspkg.Descriptor {
	builtinNames := builtinSlashCommandNames()
	catalog := make([]commandspkg.Descriptor, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	orderedSkills := append([]skillspkg.Skill(nil), skills...)
	sort.SliceStable(orderedSkills, func(i, j int) bool {
		return strings.ToLower(strings.TrimSpace(orderedSkills[i].Name)) < strings.ToLower(strings.TrimSpace(orderedSkills[j].Name))
	})
	for _, skill := range orderedSkills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := builtinNames[key]; exists {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = "Invoke the " + name + " skill"
		}
		catalog = append(catalog, commandspkg.Descriptor{
			Name:           name,
			Description:    description,
			Usage:          "/" + name + " [instructions]",
			TakesArguments: true,
		})
	}
	return catalog
}

func slashCommandCatalog(cwd string) ([]commandspkg.Descriptor, error) {
	catalog := builtinSlashCommandCatalog()
	skills, err := skillspkg.LoadAll(cwd)
	if len(skills) == 0 {
		return catalog, err
	}
	return append(catalog, skillSlashCommandCatalog(skills)...), err
}

func slashCommandDescriptors(cwd string) ([]ipc.SlashCommandDescriptorPayload, error) {
	catalog, err := slashCommandCatalog(cwd)
	return commandspkg.Descriptors(catalog), err
}

func lookupSlashSkill(cwd string, command string) (skillspkg.Skill, bool, error) {
	if _, exists := builtinSlashCommandNames()[strings.ToLower(strings.TrimSpace(command))]; exists {
		return skillspkg.Skill{}, false, nil
	}
	skills, err := skillspkg.LoadAll(cwd)
	skill, ok := skillspkg.LookupByName(skills, command)
	if !ok {
		return skillspkg.Skill{}, false, err
	}
	return skill, true, err
}
