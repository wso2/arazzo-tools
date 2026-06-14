package loader

import (
	"path/filepath"
	"testing"
)

func TestIsRemoteURL(t *testing.T) {
	cases := map[string]bool{
		"http://x.com/a.yaml":  true,
		"https://x.com/a.yaml": true,
		"HTTPS://x.com/a.yaml": true,
		"./a.yaml":             false,
		"/abs/a.yaml":          false,
		"a.yaml":               false,
	}
	for in, want := range cases {
		if got := IsRemoteURL(in); got != want {
			t.Errorf("IsRemoteURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestResolveBaseURI(t *testing.T) {
	retrieval := filepath.Join("a", "b", "wf.arazzo.yaml")
	cases := []struct {
		name      string
		self      string
		retrieval string
		want      string
	}{
		{"absent-local", "", retrieval, retrieval},
		{"absolute-remote", "https://api.example.com/wf.yaml", retrieval, "https://api.example.com/wf.yaml"},
		{"relative-local", "nested/wf.yaml", retrieval, filepath.Join("a", "b", "nested", "wf.yaml")},
		{"relative-against-remote-retrieval", "wf.yaml", "https://api.example.com/dir/root.yaml", "https://api.example.com/dir/wf.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveBaseURI(tc.self, tc.retrieval); got != tc.want {
				t.Errorf("ResolveBaseURI(%q,%q) = %q, want %q", tc.self, tc.retrieval, got, tc.want)
			}
		})
	}
}

func TestResolveSourceLocation(t *testing.T) {
	cases := []struct {
		name       string
		base       string
		sourceURL  string
		wantTarget string
		wantRemote bool
	}{
		{
			name: "local-relative", base: filepath.Join("a", "b", "wf.yaml"), sourceURL: "./api.yaml",
			wantTarget: filepath.Join("a", "b", "api.yaml"), wantRemote: false,
		},
		{
			name: "remote-base-relative-source", base: "https://api.example.com/wf/root.arazzo.yaml", sourceURL: "./device.yaml",
			wantTarget: "https://api.example.com/wf/device.yaml", wantRemote: true,
		},
		{
			name: "remote-base-parent-relative", base: "https://api.example.com/wf/root.arazzo.yaml", sourceURL: "../shared/api.yaml",
			wantTarget: "https://api.example.com/shared/api.yaml", wantRemote: true,
		},
		{
			name: "absolute-remote-source-wins", base: filepath.Join("a", "b", "wf.yaml"), sourceURL: "https://other.com/a.yaml",
			wantTarget: "https://other.com/a.yaml", wantRemote: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotTarget, gotRemote := ResolveSourceLocation(tc.base, tc.sourceURL)
			if gotTarget != tc.wantTarget || gotRemote != tc.wantRemote {
				t.Errorf("ResolveSourceLocation(%q,%q) = (%q,%v), want (%q,%v)",
					tc.base, tc.sourceURL, gotTarget, gotRemote, tc.wantTarget, tc.wantRemote)
			}
		})
	}
}
