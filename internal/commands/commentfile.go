package commands

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/infracost/ci/internal/vcsurl"
	"github.com/infracost/vcs/pkg/vcs"
	"github.com/infracost/vcs/pkg/vcs/comment"
)

// fileVCS renders the comment and writes it to a path instead of a pull
// request, for CI jobs that cannot hold a PR-write token. It implements
// vcs.VCS so diff keeps one code path.
type fileVCS struct {
	path     string
	tag      string
	provider string
	out      *os.File
}

// stdoutPath is the conventional "write to stdout" path. Nothing else in the
// diff path writes to stdout, so the pipe stays clean.
const stdoutPath = "-"

// defaultCommentTag matches the vcs module's default, which each provider
// applies itself when Options.Tag is empty.
const defaultCommentTag = "infracost-comment"

// fileMaxCommentSize stands in for a provider limit a file does not have.
// Large enough to never truncate, small enough not to overflow the buffer
// arithmetic in comment.Render.
const fileMaxCommentSize = math.MaxInt32

func newFileVCS(path, tag, provider string) *fileVCS {
	if tag == "" {
		tag = defaultCommentTag
	}
	return &fileVCS{path: path, tag: tag, provider: provider, out: os.Stdout}
}

// GenerateComment renders the flat template: the destination is unknown, so
// HTML cannot be assumed to render.
func (f *fileVCS) GenerateComment(data comment.Data) (string, error) {
	size, unit := f.MaxCommentSize()
	return comment.Render(comment.FlatTemplate, size, unit, f.SourceLink, data)
}

// PostComment writes the body and reports it as not posted, so nothing
// downstream records a pull request comment that does not exist.
func (f *fileVCS) PostComment(_ context.Context, body string, _ vcs.Behavior) (vcs.PostResult, error) {
	tagged := f.addTags(body)

	if f.path == stdoutPath {
		if _, err := fmt.Fprintln(f.out, tagged); err != nil {
			return vcs.PostResult{}, fmt.Errorf("failed to write the comment to stdout: %w", err)
		}
		return vcs.PostResult{SkipReason: "comment written to stdout"}, nil
	}

	if err := f.writeFile(tagged); err != nil {
		return vcs.PostResult{}, fmt.Errorf("failed to write the comment to %s: %w", f.path, err)
	}
	return vcs.PostResult{SkipReason: fmt.Sprintf("comment written to %s", f.path)}, nil
}

// MaxCommentSize returns an effectively unlimited size: a file has no API
// limit, so truncating would invent a constraint.
func (f *fileVCS) MaxCommentSize() (int, comment.SizeUnit) {
	return fileMaxCommentSize, comment.SizeUnitBytes
}

// SourceLink returns empty: the destination is not a provider web UI, so
// file and line links are omitted.
func (f *fileVCS) SourceLink(_, _, _ string, _ int) string {
	return ""
}

// addTags applies the tag each provider adds inside its own PostComment.
// Without it the job that posts the file leaves a comment no later run can
// find, so every run adds another one.
func (f *fileVCS) addTags(body string) string {
	now := time.Now()
	// Bitbucket strips markdown comments, so its tag has to be visible.
	if f.provider == vcsurl.ProviderBitbucket {
		return vcs.AddFooterTags(body, f.tag, &now)
	}
	return vcs.AddMarkdownTags(body, f.tag, &now)
}

// writeFile writes through a temp file in the same directory. The rename is
// atomic, so a failed write leaves the previous comment intact, and it
// replaces a symlink at the path instead of writing through it.
func (f *fileVCS) writeFile(body string) error {
	tmp, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".*")
	if err != nil {
		return err
	}
	//nolint:gosec // G703: the operator chose f.path with --comment-out-file, and this only removes the temp file beside it
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	//nolint:gosec // G703: writing to the operator's chosen path is the flag's purpose
	return os.Rename(tmp.Name(), f.path)
}
