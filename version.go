package main

import (
	_ "embed"
	"regexp"
	"runtime/debug"
)

// The manifest is the one place the version is written; a release tag must
// match it, so the binary built from that tag says the same.
//
//go:embed herdr-plugin.toml
var manifest string

var manifestVersion = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`)

// version is the manifest's version, and the commit when go stamped one
// (built from a git checkout): "0.1.0 · 883abc6", "-dirty" with local changes.
func version() string {
	v := "dev"
	if m := manifestVersion.FindStringSubmatch(manifest); m != nil {
		v = m[1]
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return v
	}
	v += " · " + rev[:min(7, len(rev))]
	if dirty {
		v += "-dirty"
	}
	return v
}
