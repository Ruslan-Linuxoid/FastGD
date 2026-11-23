package main

import (
	"runtime/debug"
	"testing"
)

func TestTheVersionOfABuild(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}
	inGit := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261123153633-78c0f326329b+dirty"},
		Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "78c0f32"}}}
	for _, c := range []struct {
		name, linked string
		bi           *debug.BuildInfo
		want         string
	}{
		{"linked", "v0.2.0", installed, "v0.2.0"},
		{"go install", "dev", installed, "v0.1.0"},
		{"in git", "dev", inGit, "dev"},
		{"devel", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"no information", "dev", nil, "dev"},
	} {
		if got := versionOf(c.linked, c.bi); got != c.want {
			t.Errorf("%s: the version is %q, want %q", c.name, got, c.want)
		}
	}
}
