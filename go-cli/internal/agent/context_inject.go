package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SystemContext holds session-stable context (cached once per session).
type SystemContext struct {
	MainBranch string
	GitUser    string
}

// TurnContext holds volatile context refreshed every user turn.
type TurnContext struct {
	CurrentDir       string
	GitBranch        string
	GitStatus        string
	RecentLog        string
	DirectoryListing string
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
	ctx.DirectoryListing = listDirectory(ctx.CurrentDir)
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
	if turn.DirectoryListing != "" {
		b.WriteString("\nFiles and directories in working directory:\n" + turn.DirectoryListing + "\n")
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

// listDirectory returns a compact listing of the working directory (two levels deep).
func listDirectory(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			b.WriteString(name + "/\n")
			subEntries, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for _, se := range subEntries {
				seName := se.Name()
				if strings.HasPrefix(seName, ".") {
					continue
				}
				if se.IsDir() {
					b.WriteString("  " + seName + "/\n")
				} else {
					b.WriteString("  " + seName + "\n")
				}
			}
		} else {
			b.WriteString(name + "\n")
		}
	}
	listing := strings.TrimSpace(b.String())
	if len(listing) > 3000 {
		listing = listing[:3000] + "\n[truncated]"
	}
	return listing
}
