package commands

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnknownInputHint(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		err      error
		want     []string
		wantNone bool
	}{
		{
			name: "a v0.1 command names its replacement",
			err:  errors.New(`unknown command "breakdown" for "infracost-ci"`),
			want: []string{"Infracost CLI v0.1 command", "'diff' replaces it", "infracost-ci diff --base-path"},
		},
		{
			name: "a v0.1 command with no replacement still says so",
			err:  errors.New(`unknown command "configure" for "infracost-ci"`),
			want: []string{"Infracost CLI v0.1 command", "infracost-ci diff --base-path"},
		},
		{
			name: "an unrecognised command gets the example alone",
			err:  errors.New(`unknown command "wibble" for "infracost-ci"`),
			want: []string{"infracost-ci diff --base-path", ciDocsURL},
		},
		{
			name: "an unknown flag points at the command's own help, not at diff",
			err:  errors.New("unknown flag: --terraform-plan-flags"),
			want: []string{"--help for the flags it takes", ciDocsURL},
		},
		{
			name:     "a known platform is named in prose",
			platform: "github_actions",
			err:      errors.New(`unknown command "wibble" for "infracost-ci"`),
			want:     []string{"which GitHub Actions mostly fills in for you"},
		},
		{
			name:     "Azure is named without its raw repository-provider suffix",
			platform: "azure_devops_TfsGit",
			err:      errors.New(`unknown command "wibble" for "infracost-ci"`),
			want:     []string{"which Azure Pipelines mostly fills in for you"},
		},
		{
			// $CI is the last fallback in getCIPlatform, so an unrecognised CI
			// used to produce "which true mostly fills in for you".
			name:     "an unrecognised platform is not named at all",
			platform: "true",
			err:      errors.New(`unknown command "wibble" for "infracost-ci"`),
			want:     []string{"Everything else comes from the environment. See "},
		},
		{
			name:     "any other error is left alone",
			err:      errors.New("cannot determine the repository URL"),
			wantNone: true,
		},
		{
			name:     "no error is nothing to answer",
			err:      nil,
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INFRACOST_CI_PLATFORM", tt.platform)
			got := UnknownInputHint(tt.err)
			if tt.wantNone {
				assert.Empty(t, got)
				return
			}
			for _, want := range tt.want {
				assert.Contains(t, got, want)
			}
		})
	}
}
