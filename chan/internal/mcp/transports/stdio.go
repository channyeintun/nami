package transports

import (
	"os/exec"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewStdio(definition Config) sdkmcp.Transport {
	command := exec.Command(definition.Command, definition.Args...)
	command.Dir = definition.WorkingDir
	if env := mergeEnv(definition.Env); len(env) > 0 {
		command.Env = env
	}
	return &sdkmcp.CommandTransport{
		Command:           command,
		TerminateDuration: definition.ShutdownGrace,
	}
}
