package commands

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/infracost/go-proto/pkg/rat"
	"github.com/infracost/vcs/pkg/vcs"
	"github.com/infracost/vcs/pkg/vcs/comment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ vcs.VCS = (*fileVCS)(nil)

// Windows maps a file mode to a read-only bit and ignores directory mode bits
// entirely, so neither the 0600 write nor a write blocked by its directory can
// be observed there. The behaviour under test is POSIX's, and the published
// image is Linux.
func skipWithoutPOSIXPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("file and directory permissions are not enforced by mode bits on Windows")
	}
}

func TestFileVCS_PostCommentWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comment.md")

	result, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.NoError(t, err)

	// Not posted: nothing downstream may record a pull request comment that
	// does not exist.
	assert.False(t, result.Posted)
	assert.Empty(t, result.Body)
	assert.Equal(t, "comment written to "+path, result.SkipReason)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(written), "comment body")
}

// The tag every provider adds inside its own PostComment, so the job that posts
// the file leaves a comment the next run can find instead of a second one.
func TestFileVCS_PostCommentTagsTheBody(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		tag      string
		wantTag  string
	}{
		{name: "github default tag", provider: "github", wantTag: "[//]: <> (infracost-comment"},
		{name: "gitlab custom tag", provider: "gitlab", tag: "costs", wantTag: "[//]: <> (costs"},
		{name: "azure default tag", provider: "azure_repos", wantTag: "[//]: <> (infracost-comment"},
		// Bitbucket strips markdown comments, so its tag is a visible footer.
		{name: "bitbucket footer tag", provider: "bitbucket", wantTag: "*(infracost-comment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "comment.md")

			_, err := newFileVCS(path, tt.tag, tt.provider).PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
			require.NoError(t, err)

			written, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(written), tt.wantTag)
			assert.Contains(t, string(written), "valid-at=")
			assert.Contains(t, string(written), "comment body")
		})
	}
}

func TestFileVCS_PostCommentOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comment.md")
	require.NoError(t, os.WriteFile(path, []byte("a previous run's comment"), 0o600))

	_, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.NoError(t, err)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(written), "comment body")
	assert.NotContains(t, string(written), "a previous run's comment")
}

// The comment may quote costs from a private repository, so an existing
// world-readable file is replaced, not written into.
func TestFileVCS_PostCommentTightensPermissions(t *testing.T) {
	skipWithoutPOSIXPermissions(t)

	path := filepath.Join(t.TempDir(), "comment.md")
	require.NoError(t, os.WriteFile(path, []byte("pre-created by the job"), 0o644))

	_, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// The path sits in a checkout a fork pull request controls, so a symlink there
// must not redirect the write.
func TestFileVCS_PostCommentDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0o600))

	path := filepath.Join(dir, "comment.md")
	require.NoError(t, os.Symlink(target, path))

	_, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.NoError(t, err)

	unchanged, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(unchanged))

	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&os.ModeSymlink, "the symlink should have been replaced by a regular file")
}

// A failed write leaves no half-written comment for a later job to post.
func TestFileVCS_PostCommentLeavesNoPartialFile(t *testing.T) {
	skipWithoutPOSIXPermissions(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this case needs")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "comment.md")
	require.NoError(t, os.WriteFile(path, []byte("a previous run's comment"), 0o600))

	// A directory in the temp file's place is the write failing before rename.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.Error(t, err)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "a previous run's comment", string(written))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temp file left behind")
}

func TestFileVCS_PostCommentToStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	client := newFileVCS(stdoutPath, "", "github")
	client.out = f

	result, err := client.PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.NoError(t, err)
	assert.False(t, result.Posted)
	assert.Equal(t, "comment written to stdout", result.SkipReason)

	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(written), "comment body")
	assert.True(t, strings.HasSuffix(string(written), "\n"))
}

func TestFileVCS_PostCommentUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "comment.md")

	_, err := newFileVCS(path, "", "github").PostComment(context.Background(), "comment body", vcs.BehaviorUpdate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write the comment to")
}

func TestFileVCS_GenerateCommentIsFlatAndUnlinked(t *testing.T) {
	body, err := newFileVCS("comment.md", "", "github").GenerateComment(comment.Data{
		Currency:             "USD",
		RepoURL:              "https://github.com/org/repo",
		CommitSHA:            "abc123",
		TotalMonthlyCost:     rat.New(150),
		PastTotalMonthlyCost: rat.New(100),
		Projects: []comment.ProjectResult{{
			Name:                 "project",
			TotalMonthlyCost:     rat.New(150),
			PastTotalMonthlyCost: rat.New(100),
		}},
	})
	require.NoError(t, err)

	// The destination is unknown, so the flat template renders no HTML.
	for _, tag := range []string{"<details>", "<summary>", "<img", "<table"} {
		assert.NotContains(t, body, tag)
	}
	// SourceLink returns empty, so no file or line link is rendered.
	assert.NotContains(t, body, "github.com/org/repo/blob")
}

func TestFileVCS_MaxCommentSizeDoesNotTruncate(t *testing.T) {
	size, unit := newFileVCS("comment.md", "", "github").MaxCommentSize()

	assert.Equal(t, fileMaxCommentSize, size)
	assert.Equal(t, comment.SizeUnitBytes, unit)
}

func TestFileVCS_SourceLinkIsEmpty(t *testing.T) {
	assert.Empty(t, newFileVCS("comment.md", "", "github").SourceLink("https://github.com/a/b", "sha", "main.tf", 1))
}
