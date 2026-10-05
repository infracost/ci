package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Both tests below plant a shell command in a repository's config and watch for
// it to run. Git for Windows resolves that through its own bundled sh, where a
// Go filepath is backslash-escaped away, so the control cannot arm and the
// assertion would pass having proved nothing. What is under test is git's own
// -c precedence, which is identical on every platform, and the image these
// calls are hardened for is Linux.
func skipWithoutPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("planted config commands do not execute under Git for Windows")
	}
}

// The image sets safe.directory '*', so a mounted .git/config is read. None of
// these keys fires on rev-parse or log as they are invoked today — only an
// index-refreshing command reaches core.fsmonitor, and core.pager needs a TTY.
// The guard is there so a call site added later is covered by construction,
// which is what this asserts: the control proves the plant is live.
func TestHardenedArgsIgnoreRepoConfigExecKeys(t *testing.T) {
	skipWithoutPOSIXShell(t)

	repo := initRepo(t)
	canaryDir := t.TempDir()
	canary := filepath.Join(canaryDir, "fired")
	gitConfig(t, repo, "core.fsmonitor", "touch "+canary)

	run(t, repo, "status", "--porcelain")
	require.FileExists(t, canary, "control: the planted config must execute unhardened")
	require.NoError(t, os.Remove(canary))

	run(t, repo, hardenedArgs("status", "--porcelain")...)
	require.NoFileExists(t, canary, "hardened args must outrank repo config")
}

// Planted config must not change what the metadata calls return either.
func TestRevParseAndLogUnaffectedByRepoConfig(t *testing.T) {
	skipWithoutPOSIXShell(t)

	repo := initRepo(t)
	canary := filepath.Join(t.TempDir(), "fired")
	for _, key := range []string{"core.fsmonitor", "core.pager"} {
		gitConfig(t, repo, key, "touch "+canary)
	}
	gitConfig(t, repo, "log.showSignature", "true")
	gitConfig(t, repo, "gpg.program", "touch "+canary)

	sha := RevParse(repo, "HEAD")
	require.Len(t, sha, 40)
	require.Equal(t, "seed", Log(repo, sha, "%s"))
	require.NoFileExists(t, canary)
}

func initRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	run(t, dir, "init", "-q")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600))
	run(t, dir, "add", "f")
	run(t, dir, "commit", "-qm", "seed")

	return dir
}

func gitConfig(t *testing.T, dir, key, value string) {
	t.Helper()
	run(t, dir, "config", key, value)
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	runWithEnv(t, dir, nil, args...)
}

func runWithEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test-controlled args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// A rebase or cherry-pick preserves %aI and rewrites %cI, so a run ordered on
// the author date would be stamped with the original commit's time.
func TestGetCommitInfo_CommitterTimestampIsSeparateFromAuthorDate(t *testing.T) {
	repo := initRepo(t)
	runWithEnv(t, repo, []string{"GIT_COMMITTER_DATE=2021-02-03T04:05:06+00:00"},
		"commit", "-q", "--allow-empty", "--date", "2020-01-02T03:04:05+00:00", "-m", "backdated")

	info := GetCommitInfo(repo, RevParse(repo, "HEAD"))
	requireInstant(t, "2020-01-02T03:04:05Z", info.Timestamp)
	requireInstant(t, "2021-02-03T04:05:06Z", info.CommitterTimestamp)
}

// git renders a zero offset as +00:00 or Z depending on its version, and
// GetCommitInfo passes its output through unchanged.
func requireInstant(t *testing.T, want, got string) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, got)
	require.NoError(t, err, got)
	require.Equal(t, want, parsed.UTC().Format(time.RFC3339))
}
