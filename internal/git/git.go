package git

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// HasChanges reports whether any files differ between the same relative path in
// two separate checkout directories. It diffs the working trees, not commit
// history, so shallow clones work but ignored files and .git count as changes.
func HasChanges(baseDir, headDir, path string) (bool, error) {
	basePath := baseDir
	headPath := headDir
	if path != "" && path != "." {
		basePath = filepath.Join(basePath, path)
		headPath = filepath.Join(headPath, path)
	}

	// git diff --no-index exits 1 both for "they differ" and for a path it
	// cannot read, so existence is settled here rather than from the exit code.
	baseExists, err := exists(basePath)
	if err != nil {
		return false, err
	}
	headExists, err := exists(headPath)
	if err != nil {
		return false, err
	}
	switch {
	case !baseExists && !headExists:
		return false, fmt.Errorf("%s exists in neither %s nor %s", path, baseDir, headDir)
	case baseExists != headExists:
		return true, nil
	}

	// -- so a directory named like a flag stays a path.
	cmd := exec.Command("git", "diff", "--no-index", "--quiet", "--", basePath, headPath) // #nosec G204 -- paths are controlled by caller
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return false, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff %s %s: %w: %s", basePath, headPath, err, strings.TrimSpace(stderr.String()))
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

// Only a genuine absence is false: an unreadable path must not read as a change.
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return true, nil
}
