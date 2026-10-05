package version

import (
	"runtime/debug"
	"strings"
)

var Version = "dev"

// A `go install` build gets no ldflags, so fall back to the module version the
// toolchain stamps in. Normalised to match the ldflags form, which drops the v.
func init() {
	if Version != "dev" {
		return
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return
	}
	Version = strings.TrimPrefix(bi.Main.Version, "v")
}
