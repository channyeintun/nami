package agent

import (
	"os"
	"os/exec"
	"strings"
)

// SystemContext holds session-stable context (cached once per session).
type SystemContext struct {
	MainBranch string
	GitUser    string
}

// TurnContext holds volatile context refreshed every user turn.
type TurnContext struct {
	CurrentDir string
	GitBranch  string
	GitStatus  string
	RecentLog  string
}

// LoadSystemContext gathers session-stable git info.
func LoadSystemContext() SystemContext {
	ctx := SystemContext{}
	ctx.MainBranch = gitCommand("rev-parse", "--abbrev-ref", "origin/HEAD")
	if ctx.MainBranch == "" {
		ctx.MainBranch = "main"
	} else {
		ctx.MainBranch = strings.TrimPrefix(ctx.MainBranch, "origin/")
	}
	ctx.GitUser = gitCommand("config", "user.name")
	return ctx
}

// LoadTurnContext gathers per-turn volatile context.
func LoadTurnContext() TurnContext {
	ctx := TurnContext{}
	ctx.CurrentDir, _ = os.Getwd()
	ctx.GitBranch = gitCommand("branch", "--show-current")
	status := gitCommand("status", "--short")
	if len(status) > 2000 {
		status = status[:2000] + "\n[truncated]"
	}
	ctx.GitStatus = status
	ctx.RecentLog = gitCommand("log", "--oneline", "-n", "5")
	return ctx
}

// FormatContextPrompt formats system + turn context for the system prompt.
func FormatContextPrompt(sys SystemContext, turn TurnContext) string {
	var b strings.Builder
	b.WriteString("<environment>\n")
	b.WriteString("Working directory: " + turn.CurrentDir + "\n")
	b.WriteString("Git branch: " + turn.GitBranch + "\n")
	b.WriteString("Main branch: " + sys.MainBranch + "\n")
	b.WriteString("Git user: " + sys.GitUser + "\n")
	if turn.GitStatus != "" {
		b.WriteString("\nGit status:\n" + turn.GitStatus + "\n")
	}
	if turn.RecentLog != "" {
		b.WriteString("\nRecent commits:\n" + turn.RecentLog + "\n")
	}
	b.WriteString("</environment>\n")
	return b.String()
}

func gitCommand(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
