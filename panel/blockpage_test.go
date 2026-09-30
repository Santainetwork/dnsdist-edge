package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetBlockpageEnv(t *testing.T, dir string) {
	t.Helper()
	oldFile := *flagBlockpageFile
	*flagBlockpageFile = filepath.Join(dir, "blockpage.html")
	t.Cleanup(func() { *flagBlockpageFile = oldFile })
}

func TestBlockpageDefaultServed(t *testing.T) {
	resetBlockpageEnv(t, t.TempDir())
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	serveBlockpage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "Akses Diblokir") {
		t.Fatal("default page must contain Trust+ style block text")
	}
}

func TestBlockpageCustomUploadJSON(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	resetAuthEnv(t, dir)

	req := httptest.NewRequest(http.MethodPost, "/api/blockpage", strBody(`{"html":"<h1>Punya Saya</h1>"}`))
	w := httptest.NewRecorder()
	handleBlockpage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload code = %d body=%s, want 200", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "blockpage.html")); err != nil || !bytes.Contains(b, []byte("Punya Saya")) {
		t.Fatalf("custom page not saved: %v %q", err, string(b))
	}

	// Public listener now serves the custom page.
	w2 := httptest.NewRecorder()
	serveBlockpage(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w2.Body.String(), "Punya Saya") {
		t.Fatal("custom page must be served after upload")
	}

	// API info reports custom=true.
	w3 := httptest.NewRecorder()
	handleBlockpage(w3, httptest.NewRequest(http.MethodGet, "/api/blockpage", nil))
	if !strings.Contains(w3.Body.String(), `"custom":true`) {
		t.Fatalf("info must report custom:true, got %s", w3.Body.String())
	}
}

func TestBlockpageCustomUploadMultipart(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("page", "blokir.html")
	fw.Write([]byte("<html>halaman kustom</html>"))
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/blockpage", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	handleBlockpage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("multipart upload code = %d, want 200", w.Code)
	}
	w2 := httptest.NewRecorder()
	serveBlockpage(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w2.Body.String(), "halaman kustom") {
		t.Fatal("multipart-uploaded page must be served")
	}
}

func TestBlockpageResetToDefault(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	os.WriteFile(filepath.Join(dir, "blockpage.html"), []byte("kustom"), 0o644)

	w := httptest.NewRecorder()
	handleBlockpage(w, httptest.NewRequest(http.MethodDelete, "/api/blockpage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("reset code = %d, want 200", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "blockpage.html")); !os.IsNotExist(err) {
		t.Fatal("reset must remove custom page")
	}
	w2 := httptest.NewRecorder()
	serveBlockpage(w2, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w2.Body.String(), "Akses Diblokir") {
		t.Fatal("default must be served after reset")
	}
}

func TestBlockpageUploadTooLarge(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	big := strings.Repeat("x", blockpageMaxBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/api/blockpage", strBody(`{"html":"`+big+`"}`))
	w := httptest.NewRecorder()
	handleBlockpage(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", w.Code)
	}
}

func TestBlockpageUploadEmptyRejected(t *testing.T) {
	resetBlockpageEnv(t, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/blockpage", strBody(`{"html":"  "}`))
	w := httptest.NewRecorder()
	handleBlockpage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
}

func TestBlockpageDomainPlaceholder(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	os.WriteFile(filepath.Join(dir, "blockpage.html"), []byte("<p>{{domain}}</p>"), 0o644)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "judol.example.com:80"
	w := httptest.NewRecorder()
	serveBlockpage(w, req)
	if !strings.Contains(w.Body.String(), "judol.example.com") {
		t.Fatalf("{{domain}} must be replaced with host, got %s", w.Body.String())
	}
}

func TestBlockpageMethodNotAllowed(t *testing.T) {
	resetBlockpageEnv(t, t.TempDir())
	w := httptest.NewRecorder()
	handleBlockpage(w, httptest.NewRequest(http.MethodPut, "/api/blockpage", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", w.Code)
	}
}

func TestBlockpageMirrorsNginxWebroot(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	oldWebroot := *flagBlockpageWebroot
	*flagBlockpageWebroot = filepath.Join(dir, "www", "blockpage.html")
	if err := os.MkdirAll(filepath.Dir(*flagBlockpageWebroot), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { *flagBlockpageWebroot = oldWebroot })

	w := httptest.NewRecorder()
	handleBlockpage(w, httptest.NewRequest(http.MethodPost, "/api/blockpage", strBody(`{"html":"<h1>Mirror</h1>"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("upload code = %d body=%s", w.Code, w.Body.String())
	}
	mirrored, err := os.ReadFile(*flagBlockpageWebroot)
	if err != nil || string(mirrored) != "<h1>Mirror</h1>" {
		t.Fatalf("nginx mirror = %q, err=%v", mirrored, err)
	}

	w = httptest.NewRecorder()
	handleBlockpage(w, httptest.NewRequest(http.MethodDelete, "/api/blockpage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete code = %d body=%s", w.Code, w.Body.String())
	}
	resetPage, err := os.ReadFile(*flagBlockpageWebroot)
	if err != nil || !bytes.Contains(resetPage, []byte("Akses Diblokir")) {
		t.Fatalf("nginx mirror default = %q, err=%v", resetPage, err)
	}
}

func TestBlockpageEscapesHostPlaceholder(t *testing.T) {
	dir := t.TempDir()
	resetBlockpageEnv(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "blockpage.html"), []byte("<p>{{domain}}</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = `<script>alert(1)</script>`
	w := httptest.NewRecorder()
	serveBlockpage(w, req)
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatalf("host must be escaped, got %s", w.Body.String())
	}
}

func TestSettingsPasswordIsHashed(t *testing.T) {
	dir := t.TempDir()
	resetAuthEnv(t, dir)

	w := httptest.NewRecorder()
	handleSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strBody(`{"panel_password":"new-password"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("settings code = %d body=%s", w.Code, w.Body.String())
	}
	stored, err := os.ReadFile(filepath.Join(dir, "panel.password"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(stored))
	if !isHashedPassword(got) || !verifyPassword(got, "new-password") {
		t.Fatalf("settings password must be PBKDF2 hash, got %q", got)
	}
}

func TestSettingsRejectsShortPassword(t *testing.T) {
	dir := t.TempDir()
	resetAuthEnv(t, dir)
	w := httptest.NewRecorder()
	handleSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strBody(`{"panel_password":"short"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("settings code = %d, want 400", w.Code)
	}
}
