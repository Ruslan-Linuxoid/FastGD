package main

import (
	"runtime/debug"
	"testing"
)

func TestTheVersionOfABuild(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}
	inGit := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261123153633-78c0f326329b"},
		Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "78c0f32"}}}
	afterTag := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.2-0.20261123153633-78c0f326329b"}}
	dirty := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1+dirty"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "b3580ec"}, {Key: "vcs.modified", Value: "true"}}}
	onTag := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.1"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "b3580ec"}, {Key: "vcs.modified", Value: "false"}}}
	for _, c := range []struct {
		name, linked string
		bi           *debug.BuildInfo
		want         string
	}{
		{"linked", "v0.2.0", installed, "v0.2.0"},
		{"go install", "dev", installed, "v0.1.0"},
		{"in git", "dev", inGit, "dev"},
		{"after a tag", "dev", afterTag, "dev"},
		{"with changes", "dev", dirty, "dev"},
		{"on a tag", "dev", onTag, "v0.0.1"},
		{"devel", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"no information", "dev", nil, "dev"},
	} {
		if got := versionOf(c.linked, c.bi); got != c.want {
			t.Errorf("%s: the version is %q, want %q", c.name, got, c.want)
		}
	}
}
