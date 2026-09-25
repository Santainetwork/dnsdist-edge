package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setSourceModeForTest(t *testing.T, mode string) {
	t.Helper()
	old := *flagSourceMode
	*flagSourceMode = mode
	t.Cleanup(func() { *flagSourceMode = old })
}

func TestSourceModeFlagAndValidation(t *testing.T) {
	registered := flag.Lookup("source-mode")
	if registered == nil {
		t.Fatal("-source-mode flag is not registered")
	}
	if os.Getenv("PANEL_SOURCE_MODE") == "" && registered.DefValue != "feeds" {
		t.Fatalf("source-mode default = %q, want feeds", registered.DefValue)
	}

	for _, tt := range []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{name: "feeds", mode: "feeds"},
		{name: "RPZ slave", mode: "rpz-slave"},
		{name: "empty", mode: "", wantErr: true},
		{name: "unknown", mode: "rpz", wantErr: true},
		{name: "surrounding whitespace", mode: " feeds ", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSourceMode(tt.mode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSourceMode(%q) error = %v, wantErr %v", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestHandleMasterStatusExposesSourceMode(t *testing.T) {
	setSourceModeForTest(t, "rpz-slave")
	w := httptest.NewRecorder()
	handleMasterStatus(w, httptest.NewRequest(http.MethodGet, "/api/master/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var response struct {
		SourceMode string `json:"source_mode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SourceMode != "rpz-slave" {
		t.Fatalf("source_mode = %q, want rpz-slave", response.SourceMode)
	}
}

func TestRPZSlaveRejectsPanelFeedWrites(t *testing.T) {
	setSourceModeForTest(t, "rpz-slave")
	dir := t.TempDir()
	sourcesPath := filepath.Join(dir, "sources.txt")
	if err := os.WriteFile(sourcesPath, []byte("keep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldSources := *flagSourcesFile
	*flagSourcesFile = sourcesPath
	t.Cleanup(func() { *flagSourcesFile = oldSources })

	for _, tt := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    string
	}{
		{name: "build", handler: handleMasterBuild, path: "/api/master/build"},
		{name: "sources", handler: handleMasterSources, path: "/api/master/sources", body: `{"sources":"replace.example\n"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tt.handler(w, httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body)))
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", w.Code, w.Body)
			}
			if !strings.Contains(w.Body.String(), "rpz-slave") {
				t.Fatalf("error does not explain source mode: %s", w.Body)
			}
		})
	}

	data, err := os.ReadFile(sourcesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep.example\n" {
		t.Fatalf("sources changed in rpz-slave mode: %q", data)
	}
}

func TestRPZSlaveKeepsWhitelistReadButRejectsEdit(t *testing.T) {
	setSourceModeForTest(t, "rpz-slave")
	dir := t.TempDir()
	path := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(path, []byte("keep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWhitelist := *flagWhitelistFile
	*flagWhitelistFile = path
	t.Cleanup(func() { *flagWhitelistFile = oldWhitelist })

	post := httptest.NewRecorder()
	handleMasterWhitelist(post, httptest.NewRequest(http.MethodPost, "/api/master/whitelist", strings.NewReader(`{"whitelist":"Allowed.Example.\n"}`)))
	if post.Code != http.StatusConflict || !strings.Contains(post.Body.String(), "rpz-slave") {
		t.Fatalf("POST = %d %s, want rpz-slave conflict", post.Code, post.Body)
	}
	get := httptest.NewRecorder()
	handleMasterWhitelist(get, httptest.NewRequest(http.MethodGet, "/api/master/whitelist", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"whitelist":"keep.example\n"`) {
		t.Fatalf("GET = %d %s", get.Code, get.Body)
	}
}

func TestMasterAutoBuildEnabled(t *testing.T) {
	for _, tt := range []struct {
		name       string
		master     bool
		sourceMode string
		interval   time.Duration
		want       bool
	}{
		{name: "feeds master", master: true, sourceMode: "feeds", interval: time.Hour, want: true},
		{name: "RPZ slave", master: true, sourceMode: "rpz-slave", interval: time.Hour},
		{name: "disabled interval", master: true, sourceMode: "feeds"},
		{name: "edge", sourceMode: "feeds", interval: time.Hour},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := masterAutoBuildEnabled(tt.master, tt.sourceMode, tt.interval); got != tt.want {
				t.Fatalf("masterAutoBuildEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
