package events

import (
	"os"
	"strconv"
	"strings"
)

// CIPlatform returns the CI platform detected from the environment, the same
// value reported in the events payload.
func CIPlatform() string { return getCIPlatform() }

// NormalizedCIPlatform returns the platform reported in events and run
// metadata. It is derived on each call so that a platform pinned after start
// (see config.InferVCS) is picked up.
func NormalizedCIPlatform() string {
	if platform, ok := lookupCIPlatformOverride(); ok {
		return strings.ToLower(platform)
	}
	return normalizeCIPlatform(getCIPlatform())
}

// RunMetadataCIPlatform reports a platform we cannot name as unknown, so the
// dashboard can tell "somewhere unrecognised" from "field absent". Events keep
// the empty string.
func RunMetadataCIPlatform() string {
	if platform := NormalizedCIPlatform(); platform != "" {
		return platform
	}
	return "unknown"
}

func lookupCIPlatformOverride() (string, bool) {
	platform, ok := os.LookupEnv("INFRACOST_CI_PLATFORM")
	return platform, ok && platform != ""
}

func normalizeCIPlatform(raw string) string {
	platform := strings.ToLower(raw)
	if platform == "" {
		return ""
	}
	if platform == "azure_devops_" {
		return "unknown"
	}

	if _, err := strconv.ParseBool(platform); err == nil {
		return "unknown"
	}
	switch platform {
	case "yes", "no", "on", "off":
		return "unknown"
	}

	return platform
}
