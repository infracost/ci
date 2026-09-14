package trace

import (
	"fmt"

	"github.com/infracost/ci/internal/version"
)

var (
	UserAgent = fmt.Sprintf("infracost-ci-%s", version.Version)
)
