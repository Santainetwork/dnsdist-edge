package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeWhitelist(t *testing.T) {
	longLabel := strings.Repeat("a", 64) + ".example"
	validLongLabel := strings.Repeat("a", 63) + ".example"
	longDomain := strings.Repeat("a.", 126) + "a"
	tooLongDomain := longDomain + "a"
	tests := []struct {
		name              string
		input, want       string
		invalid           []int
		count, duplicates int
	}{
		{
			name:       "canonical domain ip comment dedupe stable order",
			input:      " Example.COM.\n192.0.2.1\n# note\nexample.com\n# note\n192.0.2.1\n",
			want:       "example.com\n192.0.2.1\n# note\n# note\n",
			count:      2,
			duplicates: 2,
		},
		{
			name:  "ipv6",
			input: "2001:DB8::1\n",
			want:  "2001:db8::1\n",
			count: 1,
		},
		{
			name:    "invalid url wildcard whitespace hosts adguard",
			input:   "https://example.com\n*.example.com\nfoo bar\nexample.com/path\n0.0.0.0 example.com\n||example.com^\n",
			invalid: []int{1, 2, 3, 4, 5, 6},
		},
		{
			name:    "invalid label",
			input:   "-bad.example\nvalid.example\nbad-.example\n",
			want:    "valid.example\n",
			invalid: []int{1, 3},
			count:   1,
		},
		{
			name:  "punycode accepted",
			input: "XN--BCHER-KVA.example.\n",
			want:  "xn--bcher-kva.example\n",
			count: 1,
		},
		{
			name:    "unicode rejected",
			input:   "münchen.example\n例え.テスト\n",
			invalid: []int{1, 2},
		},
		{
			name:    "label and domain limits",
			input:   longLabel + "\n" + validLongLabel + "\n" + longDomain + "\n" + tooLongDomain + "\n",
			want:    validLongLabel + "\n" + longDomain + "\n",
			invalid: []int{1, 4},
			count:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, invalid, count, duplicates := normalizeWhitelist(tt.input)
			if got != tt.want {
				t.Fatalf("normalized = %q, want %q", got, tt.want)
			}
			if !reflect.DeepEqual(invalid, tt.invalid) {
				t.Fatalf("invalid = %v, want %v", invalid, tt.invalid)
			}
			if count != tt.count || duplicates != tt.duplicates {
				t.Fatalf("count/duplicates = %d/%d, want %d/%d", count, duplicates, tt.count, tt.duplicates)
			}
		})
	}
}

func whitelistRequest(t *testing.T, method, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "whitelist.txt")
	old := *flagWhitelistFile
	*flagWhitelistFile = path
	t.Cleanup(func() { *flagWhitelistFile = old })
	r := httptest.NewRequest(method, "/api/master/whitelist", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	handleMasterWhitelist(w, r)
	return w, path
}

func TestHandleMasterWhitelistCanonicalSaveAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(path, []byte("old.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := *flagWhitelistFile
	*flagWhitelistFile = path
	t.Cleanup(func() { *flagWhitelistFile = old })
	w := httptest.NewRecorder()
	handleMasterWhitelist(w, httptest.NewRequest(http.MethodPost, "/api/master/whitelist", strings.NewReader(`{"whitelist":" Example.COM.\nexample.com\n192.0.2.1\n"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "example.com\n192.0.2.1\n" {
		t.Fatalf("saved = %q, err = %v", data, err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != "old.example\n" {
		t.Fatalf("backup = %q, err = %v", backup, err)
	}
	var got struct {
		OK      bool `json:"ok"`
		Count   int  `json:"count"`
		Removed int  `json:"removed_duplicates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.OK || got.Count != 2 || got.Removed != 1 {
		t.Fatalf("response = %s, err = %v", w.Body, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
	backupInfo, err := os.Stat(path + ".bak")
	if err != nil || backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, err = %v", backupInfo.Mode().Perm(), err)
	}
}

func TestHandleMasterWhitelistInvalidLeavesExistingUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whitelist.txt")
	if err := os.WriteFile(path, []byte("keep.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := *flagWhitelistFile
	*flagWhitelistFile = path
	t.Cleanup(func() { *flagWhitelistFile = old })
	w := httptest.NewRecorder()
	handleMasterWhitelist(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"whitelist":"bad value\n"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep.example\n" {
		t.Fatalf("file changed: %q, %v", data, err)
	}
}

func TestHandleMasterWhitelistGET(t *testing.T) {
	w, path := whitelistRequest(t, http.MethodGet, "")
	if w.Code != http.StatusOK || w.Body.String() != "{\"count\":0,\"whitelist\":\"\"}\n" {
		t.Fatalf("missing GET = %d %q", w.Code, w.Body)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("one.example\ntwo.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handleMasterWhitelist(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count":2`) || !strings.Contains(w.Body.String(), `"whitelist":"one.example\ntwo.example\n"`) {
		t.Fatalf("GET = %d %q", w.Code, w.Body)
	}
}

func TestHandleMasterWhitelistRejectsMalformedOversizedAndWrongMethod(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		code               int
	}{
		{"invalid JSON", http.MethodPost, `{"whitelist":`, http.StatusBadRequest},
		{"not an object", http.MethodPost, `null`, http.StatusBadRequest},
		{"trailing", http.MethodPost, `{"whitelist":"a.example"}{}`, http.StatusBadRequest},
		{"unknown", http.MethodPost, `{"whitelist":"a.example","extra":true}`, http.StatusBadRequest},
		{"oversized", http.MethodPost, `{"whitelist":"` + strings.Repeat("a", 1<<20) + `"}`, http.StatusRequestEntityTooLarge},
		{"method", http.MethodDelete, "", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := whitelistRequest(t, tc.method, tc.body)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d", w.Code, tc.code)
			}
		})
	}
}

func TestRegisterMasterRoutes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		registerMasterRoutes(mux, enabled)
		for _, path := range []string{"/api/master/status", "/api/master/build", "/api/master/sources", "/api/master/whitelist"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			want := http.StatusNotFound
			if enabled {
				want = http.StatusUnauthorized
			}
			if w.Code != want {
				t.Fatalf("enabled=%v %s status=%d, want %d", enabled, path, w.Code, want)
			}
		}
	}
}
