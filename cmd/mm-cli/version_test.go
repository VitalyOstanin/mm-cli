package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"ldflags wins", "v9.9.9", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true, "v9.9.9"},
		{"module version", defaultVersion, &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true, "v1.2.3"},
		{"devel", defaultVersion, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, defaultVersion},
		{"empty", defaultVersion, &debug.BuildInfo{}, true, defaultVersion},
		{"no build info", defaultVersion, nil, false, defaultVersion},
		{"nil info with ok", defaultVersion, nil, true, defaultVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldflags, tt.info, tt.ok); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
