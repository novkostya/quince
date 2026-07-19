// Package version holds the build-stamped version of the quince binary.
package version

// Version is overridden at build time via -ldflags "-X .../version.Version=<v>".
// The default marks an un-stamped local build honestly.
var Version = "0.0.0-dev"

// String returns the current version string.
func String() string { return Version }
