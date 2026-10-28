// Package buildinfo holds version information injected at build time via
// -ldflags "-X github.com/shyper/autodownloadarr/internal/buildinfo.Version=...".
package buildinfo

// Version is the release version, "dev" for local builds.
var Version = "dev"
