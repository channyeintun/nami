package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// SystemContext holds session-stable context (cached once per session).
type SystemContext struct {
	MainBranch   string
	GitUser      string
	OS           string
	Architecture string
	MemoryFiles  []MemoryFile
}

// TurnContext holds volatile context refreshed every user turn.
type TurnContext struct {
	CurrentDir       string
	GitBranch        string
	GitStatus        string
	RecentLog        string
	DirectoryListing string
}

// LoadSystemContext gathers session-stable git info and instruction files.
func LoadSystemContext() SystemContext {
	ctx := SystemContext{}
	ctx.MainBranch = gitCommand("rev-parse", "--abbrev-ref", "origin/HEAD")
	if ctx.MainBranch == "" {
		ctx.MainBranch = "main"
	} else {
		ctx.MainBranch = strings.TrimPrefix(ctx.MainBranch, "origin/")
	}
	ctx.GitUser = gitCommand("config", "user.name")
	ctx.OS = runtime.GOOS
	ctx.Architecture = runtime.GOARCH
	ctx.MemoryFiles = LoadMemoryFiles()
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

// FormatSystemContextPrompt formats session-stable environment context.
func FormatSystemContextPrompt(sys SystemContext) string {
	var b strings.Builder
	b.WriteString("<environment>\n")
	b.WriteString("OS: " + sys.OS + "\n")
	b.WriteString("Architecture: " + sys.Architecture + "\n")
	b.WriteString("Main branch: " + sys.MainBranch + "\n")
	b.WriteString("Git user: " + sys.GitUser + "\n")
	b.WriteString("</environment>\n")
	return b.String()
}

// FormatTurnContextPrompt formats per-turn working context for transient injection.
func FormatTurnContextPrompt(turn TurnContext) string {
	var b strings.Builder
	b.WriteString("<working_context>\n")
	b.WriteString("Present working directory (pwd): " + turn.CurrentDir + "\n")
	b.WriteString("Git branch: " + firstNonEmptyContext(turn.GitBranch, "(not on a git branch)") + "\n")
	if turn.GitStatus != "" {
		b.WriteString("\nGit status:\n" + turn.GitStatus + "\n")
	}
	if turn.RecentLog != "" {
		b.WriteString("\nRecent commits:\n" + turn.RecentLog + "\n")
	}
	if turn.DirectoryListing != "" {
		b.WriteString("\nFiles and directories in working directory:\n" + turn.DirectoryListing + "\n")
	}
	b.WriteString("</working_context>\n")
	return b.String()
}

// FormatContextPrompt formats the combined environment context.
func FormatContextPrompt(sys SystemContext, turn TurnContext) string {
	return strings.TrimSpace(FormatSystemContextPrompt(sys) + "\n\n" + FormatTurnContextPrompt(turn))
}

func firstNonEmptyContext(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
