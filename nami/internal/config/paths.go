package config

import (
	"os"
	"path/filepath"
	"strings"
)

const appConfigDirName = "nami"

// ConfigDir returns the platform-correct configuration root for Nami.
func ConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			return filepath.Join(dir, appConfigDirName)
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		home = strings.TrimSpace(home)
		if home != "" {
			return filepath.Join(home, ".config", appConfigDirName)
		}
	}

	return filepath.Join(".", "."+appConfigDirName)
}

func SessionsDir() string {
	return filepath.Join(ConfigDir(), "sessions")
}

func HooksDir() string {
	return filepath.Join(ConfigDir(), "hooks")
}

func GlobalSkillDir() string {
	return filepath.Join(ConfigDir(), "agents")
}

// GlobalWorkflowDir holds the user's saved workflow scripts, which every
// project can run by name.
func GlobalWorkflowDir() string {
	return filepath.Join(ConfigDir(), "workflows")
}

func ArtifactsDir() string {
	return filepath.Join(ConfigDir(), "artifacts")
}

func MemoryDir() string {
	return filepath.Join(ConfigDir(), "memory")
}

func ProjectsDir() string {
	return filepath.Join(ConfigDir(), "projects")
}

func CacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			return filepath.Join(dir, appConfigDirName)
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		home = strings.TrimSpace(home)
		if home != "" {
			return filepath.Join(home, ".cache", appConfigDirName)
		}
	}

	return filepath.Join(".", "."+appConfigDirName, "cache")
}
