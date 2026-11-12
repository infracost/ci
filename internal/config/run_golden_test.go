package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	pkgscanner "github.com/infracost/cli/pkg/scanner"
	"github.com/infracost/go-proto/pkg/rat"
	"github.com/infracost/proto/gen/go/infracost/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden rewrites the golden files instead of comparing against them.
var updateGolden = flag.Bool("update-golden", false, "update the golden files in testdata")

// The dashboard reads the JSON the upload serialises, not the Go struct, so the
// golden file locks the wire shape itself. See TRK-1542.
func TestBuildRunInput_BreakdownJSONGolden(t *testing.T) {
	input := BuildRunInput(RunInputOptions{
		BaseResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{
				Name:             "project-a",
				TotalMonthlyCost: rat.New(35),
				Resources:        goldenBaseResources(),
			}},
		},
		HeadResult: &DirectoryResult{
			Projects: []pkgscanner.ProjectResult{{
				Name:             "project-a",
				TotalMonthlyCost: rat.New(32),
				Resources:        goldenHeadResources(),
			}},
		},
	})

	require.Len(t, input.ProjectResults, 1)

	assertGolden(t, "breakdown.json", struct {
		Breakdown any `json:"breakdown"`
		Diff      any `json:"diff"`
	}{
		Breakdown: input.ProjectResults[0].Breakdown,
		Diff:      input.ProjectResults[0].Diff,
	})
}

// goldenBaseResources is the base side: web carries a sub-resource that head
// drops, and api is removed outright.
func goldenBaseResources() []*provider.Resource {
	web := goldenPriced("aws_instance.web", "10")
	web.ChildResources = []*provider.Resource{goldenVolume("root_block_device", "5")}

	return []*provider.Resource{web, goldenPriced("aws_instance.api", "20")}
}

// goldenHeadResources covers every routing branch: a changed resource, an added
// one, a free resource and an unsupported one that must not be uploaded at all.
func goldenHeadResources() []*provider.Resource {
	web := goldenPriced("aws_instance.web", "25")
	web.Tagging = &provider.Tagging{
		Tags: []*provider.Tag{
			{Key: "env", Value: "prod"},
			{Key: "owner", Value: "platform", IsDefault: true},
		},
	}

	return []*provider.Resource{
		web,
		goldenPriced("aws_instance.new", "7"),
		{Name: "aws_iam_role.app", Type: "aws_iam_role", IsFree: true},
		{Name: "aws_unknown.thing", Type: "aws_unknown"},
	}
}

func goldenPriced(name, monthlyCost string) *provider.Resource {
	r := priced(name, monthlyCost)
	r.Metadata = &provider.ResourceMetadata{
		DeepChecksum:        "deep-" + name,
		DefaultTagsChecksum: "tags-" + name,
	}
	return r
}

func goldenVolume(name, monthlyCost string) *provider.Resource {
	r := goldenPriced(name, monthlyCost)
	r.Type = "aws_ebs_volume"
	r.Tagging = &provider.Tagging{
		Tags: []*provider.Tag{{Key: "env", Value: "prod"}},
	}
	return r
}

// assertGolden compares v's JSON against testdata/<name>, or rewrites it under
// -update-golden.
func assertGolden(t *testing.T, name string, v any) {
	t.Helper()

	got, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	path := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, got, 0o600))
		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing, run: go test ./internal/config/ -update-golden")

	// Git checks the file out with CRLF on Windows, so compare on LF either way.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))

	assert.Equal(t, string(want), string(got), "wire shape changed, rerun with -update-golden if intended")
}
