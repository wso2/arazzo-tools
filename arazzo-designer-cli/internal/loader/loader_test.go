package loader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

const sampleOpenAPI = `openapi: 3.0.0
info:
  title: Sample API
paths: {}
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func assertLoaded(t *testing.T, sources map[string]interface{}, name string) {
	t.Helper()
	spec, ok := sources[name].(map[string]interface{})
	if !ok {
		t.Fatalf("source %q not loaded as a map: %#v", name, sources[name])
	}
	if spec["openapi"] != "3.0.0" {
		t.Errorf("source %q has unexpected content: %#v", name, spec)
	}
}

// $self absent: relative source resolves against the Arazzo file's directory (v1.0.x behavior).
func TestLoadSources_NoSelf_Local(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "api.yaml"), sampleOpenAPI)

	doc := &models.ArazzoDoc{
		SourceDescriptions: []models.SourceDescription{{Name: "api", URL: "./api.yaml", Type: "openapi"}},
	}
	sources, err := LoadSourceDescriptions(doc, filepath.Join(dir, "root.arazzo.yaml"))
	if err != nil {
		t.Fatalf("LoadSourceDescriptions: %v", err)
	}
	assertLoaded(t, sources, "api")
}

// $self relative: base directory shifts to $self's location, and sources resolve from there.
func TestLoadSources_RelativeSelf_Local(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "api.yaml"), sampleOpenAPI)

	doc := &models.ArazzoDoc{
		Self:               "sub/root.arazzo.yaml",
		SourceDescriptions: []models.SourceDescription{{Name: "api", URL: "./api.yaml", Type: "openapi"}},
	}
	sources, err := LoadSourceDescriptions(doc, filepath.Join(dir, "root.arazzo.yaml"))
	if err != nil {
		t.Fatalf("LoadSourceDescriptions: %v", err)
	}
	assertLoaded(t, sources, "api")
}

// $self absolute (remote): a relative source URL resolves to a remote URL and is fetched.
// Two sources resolving to the same target are loaded only once (identity over location).
func TestLoadSources_RemoteSelf_AndDedup(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wf/api.yaml" {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte(sampleOpenAPI))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	doc := &models.ArazzoDoc{
		Self: srv.URL + "/wf/root.arazzo.yaml",
		SourceDescriptions: []models.SourceDescription{
			{Name: "api", URL: "./api.yaml", Type: "openapi"},
			{Name: "apiDup", URL: "./api.yaml", Type: "openapi"},
		},
	}
	// arazzoPath is irrelevant because $self is absolute.
	sources, err := LoadSourceDescriptions(doc, "ignored.arazzo.yaml")
	if err != nil {
		t.Fatalf("LoadSourceDescriptions: %v", err)
	}
	assertLoaded(t, sources, "api")
	assertLoaded(t, sources, "apiDup")
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected the remote spec to be fetched once (dedup), got %d hits", got)
	}
	// _source_url must be the RESOLVED absolute URL (not the raw "./api.yaml"), so that
	// downstream relative server-URL resolution still has a usable absolute base.
	wantSourceURL := srv.URL + "/wf/api.yaml"
	if got := sources["api"].(map[string]interface{})["_source_url"]; got != wantSourceURL {
		t.Errorf("_source_url = %v, want resolved %q", got, wantSourceURL)
	}
}

// Backward compatibility: an absolute remote source URL is fetched as-is regardless of $self.
func TestLoadSources_AbsoluteRemoteSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(sampleOpenAPI))
	}))
	defer srv.Close()

	doc := &models.ArazzoDoc{
		SourceDescriptions: []models.SourceDescription{{Name: "api", URL: srv.URL + "/api.yaml", Type: "openapi"}},
	}
	sources, err := LoadSourceDescriptions(doc, filepath.Join(t.TempDir(), "root.arazzo.yaml"))
	if err != nil {
		t.Fatalf("LoadSourceDescriptions: %v", err)
	}
	assertLoaded(t, sources, "api")
}
