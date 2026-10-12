// Package version resolves the build versions the panel displays.
package version

import (
	"runtime/debug"
	"strings"

	"github.com/pocketbase/pocketbase"
)

const (
	// Panel is the PocketAdmin release version. Bump it here.
	Panel = "0.1.0"

	// pocketBaseModule is the dependency path recorded in the build info.
	pocketBaseModule = "github.com/pocketbase/pocketbase"

	// placeholderVersion is the value PocketBase initialises its Version
	// variable to. The official PocketBase CLI overwrites it at link time with
	// -ldflags "-X github.com/pocketbase/pocketbase.Version=<version>". A plain
	// `go build` of a program that embeds PocketBase as a library does not, so
	// the placeholder would otherwise reach the UI.
	placeholderVersion = "(untracked)"

	// unknownVersion is shown when no version can be resolved at all.
	unknownVersion = "unknown"
)

// PocketBase returns the version of the embedded PocketBase library.
//
// The module version recorded in the build info is authoritative: it comes from
// go.mod and is always correct for the code actually linked into the binary.
// Only if the build info is unavailable does it fall back to PocketBase's own
// Version variable, and only when that variable holds a real value rather than
// its placeholder.
func PocketBase() string {
	if v := moduleVersion(pocketBaseModule); v != "" {
		return v
	}

	if v := pocketbase.Version; v != "" && v != placeholderVersion {
		return v
	}

	return unknownVersion
}

// moduleVersion reads the resolved version of an embedded dependency.
func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	for _, dep := range info.Deps {
		if dep.Path != path {
			continue
		}

		// A replace directive points at the code that was actually compiled.
		if dep.Replace != nil {
			return normalise(dep.Replace.Version)
		}

		return normalise(dep.Version)
	}

	return ""
}

// normalise trims the leading v and rejects build-info pseudo versions such as
// "(devel)", so the caller can fall through to another source.
func normalise(v string) string {
	v = strings.TrimPrefix(v, "v")
	if v == "" || strings.HasPrefix(v, "(") {
		return ""
	}
	return v
}

// buildTime and commit are set at link time:
//
//	go build -ldflags "-X pocketadmin/internal/version.buildTime=... -X pocketadmin/internal/version.commit=..."
//
// They are empty in a plain build, and the panel says so rather than showing a
// placeholder that looks like a value.
var (
	buildTime string
	commit    string
)

// BuildTime returns when the binary was built, or "".
func BuildTime() string { return buildTime }

// Commit returns the revision the binary was built from, or "".
func Commit() string { return commit }

// UpdateChecksEnabled reports whether this build offers to check for a newer
// release.
//
// It is off unless an update feed is configured, because a version check is an
// outbound request that names the installation and its version to a third party,
// and turning that on without being asked is not a decision this platform should
// make on an operator's behalf.
func UpdateChecksEnabled() bool { return updateFeedURL != "" }

// updateFeedURL is set at link time when a release feed exists.
var updateFeedURL string
