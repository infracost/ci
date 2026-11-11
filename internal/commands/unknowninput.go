package commands

import (
	"strings"
)

// v01Commands are the CLI v0.1 commands most often carried over into a CI v2
// job, with what to run instead. A v0.1 pipeline pasted onto this image fails
// on the command name before any of its flags are read.
var v01Commands = map[string]string{
	"breakdown":  "diff",
	"output":     "diff",
	"comment":    "diff",
	"upload":     "scan",
	"configure":  "",
	"auth":       "",
	"register":   "",
	"generate":   "",
	"completion": "",
	"feedback":   "",
	"projects":   "",
	"budgets":    "",
	"guardrails": "",
	"policies":   "",
}

// UnknownInputHint answers an unknown command or flag with a worked example
// for the detected CI platform. The root command silences both the usage block
// and the error, so without this the user gets the one-line cobra message and
// nothing else, and a job carried over from CI v0.1 fails here before anything
// else it got wrong is reached.
//
// Returns "" when the error is not an unknown command or flag.
func UnknownInputHint(err error) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	unknownCommand := strings.HasPrefix(message, "unknown command")
	unknownFlag := strings.HasPrefix(message, "unknown flag") || strings.HasPrefix(message, "unknown shorthand flag")
	if !unknownCommand && !unknownFlag {
		return ""
	}

	var b strings.Builder

	// An unknown flag is a typo on a command that exists, so the worked diff
	// example below would point away from whatever the user was running.
	if unknownFlag {
		b.WriteString("\nRun the command with --help for the flags it takes. See " + ciDocsURL)
		return b.String()
	}

	if replacement, ok := v01Commands[unknownCommandName(message)]; ok {
		b.WriteString("\nThis looks like an Infracost CLI v0.1 command. CI v2 runs the whole pull request flow in one command")
		if replacement != "" {
			b.WriteString(", so '" + replacement + "' replaces it")
		}
		b.WriteString(".\n")
	}

	b.WriteString("\nA pull request run is:\n")
	b.WriteString("  infracost-ci diff --base-path <base checkout> --head-path <head checkout>\n")
	b.WriteString("\nEverything else comes from the environment")
	if platform := platformName(); platform != "" {
		b.WriteString(", which " + platform + " mostly fills in for you")
	}
	b.WriteString(". See " + ciDocsURL)

	return b.String()
}

// ciDocsURL is the CI v2 setup guide, which carries a full job for each
// platform. Named rather than reproduced: a job is too long for an error.
const ciDocsURL = "https://www.infracost.io/docs/integrations/cicd/"

// unknownCommandName reads the name out of cobra's message, which is
// `unknown command "breakdown" for "infracost-ci"`.
func unknownCommandName(message string) string {
	_, rest, ok := strings.Cut(message, `"`)
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return name
}
