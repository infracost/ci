package trace

import (
	"fmt"

	"github.com/infracost/ci/v2/internal/version"
)

var (
	UserAgent = fmt.Sprintf("infracost-ci-%s", version.Version)
)
