package git

import (
	"os/exec"
	"strings"
)

type CommitInfo struct {
	Message     string
	AuthorName  string
	AuthorEmail string
	Timestamp   string
}

func GetCommitInfo(dir, sha string) CommitInfo {
	if sha == "" {
		return CommitInfo{}
	}
	return CommitInfo{
		Message:     Log(dir, sha, "%s"),
		AuthorName:  Log(dir, sha, "%aN"),
		AuthorEmail: Log(dir, sha, "%aE"),
		Timestamp:   Log(dir, sha, "%aI"),
	}
}

// hardenedArgs prefixes a git invocation with the exec keys reachable from
// rev-parse and log. The image sets safe.directory '*', so git will read
// .git/config from a mounted checkout; -c outranks repo config, system config
// does not. Extend this if a new subcommand is added.
func hardenedArgs(args ...string) []string {
	return append([]string{
		"-c", "core.fsmonitor=false",
		"-c", "core.pager=cat",
		"-c", "log.showSignature=false",
	}, args...)
}

func RevParse(dir string, args ...string) string {
	cmd := exec.Command("git", hardenedArgs(append([]string{"rev-parse"}, args...)...)...) // #nosec G204 -- args are controlled by caller
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func Log(dir, sha, format string) string {
	cmd := exec.Command("git", hardenedArgs("log", "-1", "--format="+format, sha)...) // #nosec G204 -- args are controlled by caller
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
