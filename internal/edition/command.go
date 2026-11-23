package edition

import (
	"context"
	"io"
	"sort"
)

// A server command is something an edition lets an operator do on the server, next to running it:
// `atrium-tablinum -config config.yaml <name> <arguments>`. It runs with the database and the edition
// started as the server would, without the web endpoints. The community edition has none.

// CommandEnv is what a command gets.
type CommandEnv struct {
	// Env is what the edition was started with (the database, the public address, the log).
	Env
	// Edition is the running edition, for the checks that need its licence.
	Edition Running
	// Args are the arguments after the name of the command.
	Args []string
	// Stdout and Stderr are where the command writes: results and secrets go to Stdout only.
	Stdout, Stderr io.Writer
}

// Command runs one server command and returns the exit status.
type Command func(ctx context.Context, env CommandEnv) int

var (
	commands   = map[string]Command{}
	standalone = map[string]bool{}
)

// RegisterCommand adds a server command. It is meant to be called from init().
func RegisterCommand(name string, c Command) { commands[name] = c }

// RegisterStandaloneCommand adds a command that does not run on the server: it gets only its arguments and the
// output streams, no database and no edition, and needs no configuration file. It is for a tool that talks to
// a server somewhere else (a client run from a CI job) and ships in the same image.
func RegisterStandaloneCommand(name string, c Command) {
	commands[name] = c
	standalone[name] = true
}

// IsStandalone says whether a command runs without the server's configuration and database.
func IsStandalone(name string) bool { return standalone[name] }

// LookupCommand finds a command by its name; nil if there is none.
func LookupCommand(name string) Command { return commands[name] }

// CommandNames lists the commands, in order.
func CommandNames() []string {
	out := make([]string, 0, len(commands))
	for n := range commands {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
