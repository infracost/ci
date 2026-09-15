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

func RevParse(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"rev-parse"}, args...)...) // #nosec G204 -- args are controlled by caller
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
	cmd := exec.Command("git", "log", "-1", "--format="+format, sha) // #nosec G204 -- args are controlled by caller
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
