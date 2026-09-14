package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHasChanges(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		headBody string
		baseOnly string
		headOnly string
		want     bool
		wantErr  bool
	}{
		{name: "identical", path: "proj", headBody: "same", want: false},
		{name: "differs", path: "proj", headBody: "different", want: true},
		{name: "empty path compares roots", path: "", headBody: "different", want: true},
		{name: "dot path compares roots", path: ".", headBody: "same", want: false},
		{name: "path added in head", path: "added", headBody: "same", headOnly: "added", want: true},
		{name: "path deleted in head", path: "removed", headBody: "same", baseOnly: "removed", want: true},
		// git exits 1 here as it does for a real diff, so the path must not read as one.
		{name: "missing path errors", path: "nope", headBody: "same", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := writeTree(t, "same")
			headDir := writeTree(t, tt.headBody)
			if tt.baseOnly != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(baseDir, tt.baseOnly), 0o750))
			}
			if tt.headOnly != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(headDir, tt.headOnly), 0o750))
			}

			got, err := HasChanges(baseDir, headDir, tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// Nested so a non-empty path exercises the join rather than the root.
func writeTree(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proj"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "proj", "main.tf"), []byte(body), 0o600))

	return dir
}
