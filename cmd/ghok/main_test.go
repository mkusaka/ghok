package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildInfo(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     string
		commit      string
		info        *debug.BuildInfo
		wantVersion string
		wantCommit  string
	}{
		{
			name: "tagged module", version: "dev",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}},
			wantVersion: "v1.2.3",
		},
		{
			name: "development module", version: "dev",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			wantVersion: "dev",
		},
		{
			name: "empty module version", version: "dev", info: &debug.BuildInfo{},
			wantVersion: "dev",
		},
		{
			name: "absent build info", version: "dev",
			wantVersion: "dev",
		},
		{
			name: "injected values win", version: "v2.0.0", commit: "injected-revision",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "build-revision"}},
			},
			wantVersion: "v2.0.0", wantCommit: "injected-revision",
		},
		{
			name: "injected values without build info", version: "v2.0.0", commit: "injected-revision",
			wantVersion: "v2.0.0", wantCommit: "injected-revision",
		},
		{
			name: "vcs revision with development module", version: "dev",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "build-revision"}},
			},
			wantVersion: "dev", wantCommit: "build-revision",
		},
		{
			name: "vcs revision with injected version", version: "v2.0.0",
			info: &debug.BuildInfo{
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "build-revision"}},
			},
			wantVersion: "v2.0.0", wantCommit: "build-revision",
		},
		{
			name: "vcs revision absent", version: "dev",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{{Key: "vcs", Value: "git"}},
			},
			wantVersion: "v1.2.3",
		},
		{
			name: "injected commit with tagged module", version: "dev", commit: "injected-revision",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v1.2.3"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "build-revision"}},
			},
			wantVersion: "v1.2.3", wantCommit: "injected-revision",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, commit := resolveBuildInfo(tc.version, tc.commit, tc.info)
			if version != tc.wantVersion || commit != tc.wantCommit {
				t.Fatalf("resolveBuildInfo() = (%q, %q), want (%q, %q)", version, commit, tc.wantVersion, tc.wantCommit)
			}
		})
	}
}
