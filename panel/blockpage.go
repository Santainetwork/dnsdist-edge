package main

import (
	_ "embed"
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// Trust+ block page hosting. One machine can serve DNS and the block page
// itself: dnsdist SpoofAction returns the sinkhole IP, and this listener (or
// the sinkhole vhost) serves the page. Optional feature — off unless the
// blockpage listener is enabled.

//go:embed static/blockpage.html
var defaultBlockpage string

const blockpageMaxBytes = 1 << 20 // 1 MiB

func customBlockpagePath() string { return *flagBlockpageFile }

// blockpageMirrorPath returns the optional nginx webroot path for the same
// HTML. When set, the panel upload mirrors the page there so the existing
// nginx :80 host keeps serving it (1 machine = DNS + blockpage).
func blockpageMirrorPath() string { return *flagBlockpageWebroot }

// writeBlockpage saves the uploaded page and mirrors it to nginx when set.
func writeBlockpage(data []byte) error {
	if err := atomicWrite(customBlockpagePath(), data, 0o644); err != nil {
		return err
	}
	if m := blockpageMirrorPath(); m != "" {
		if err := atomicWrite(m, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// resetBlockpage removes the custom page. An nginx mirror gets the embedded
// default because nginx cannot read the page embedded in this binary.
func resetBlockpage() error {
	if err := os.Remove(customBlockpagePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if m := blockpageMirrorPath(); m != "" {
		if err := atomicWriteString(m, defaultBlockpage, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// blockpageFor returns the custom page bytes or the default template.
func blockpageFor() ([]byte, bool) {
	if b, err := os.ReadFile(customBlockpagePath()); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return b, true
	}
	return []byte(defaultBlockpage), false
}

// serveBlockpage renders the block page for HTTP clients that land on the
// sinkhole. {{domain}} is replaced with the requested Host so users see which
// domain was blocked. Only the first placeholder is replaced.
func serveBlockpage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	body, _ := blockpageFor()
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		host = "situs ini"
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = html.EscapeString(host)
	if i := strings.Index(string(body), "{{domain}}"); i >= 0 {
		body = []byte(string(body[:i]) + host + string(body[i+len("{{domain}}"):]))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	w.Write(body)
}

// handleBlockpage is the panel API: GET status, POST upload (JSON {"html"} or
// multipart file field "page"), DELETE reset to default.
func handleBlockpage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_, custom := blockpageFor()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"custom":    custom,
			"path":      customBlockpagePath(),
			"max_bytes": blockpageMaxBytes,
			"listen":    *flagBlockpageListen,
			"webroot":   *flagBlockpageWebroot,
			"enabled":   *flagBlockpageListen != "" || *flagBlockpageWebroot != "",
		})
	case http.MethodPost:
		var data []byte
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "multipart/form-data"):
			mr, err := r.MultipartReader()
			if err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid multipart: "+err.Error())
				return
			}
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					jsonErr(w, http.StatusBadRequest, "multipart read: "+err.Error())
					return
				}
				if part.FormName() == "page" || part.FormName() == "file" {
					data, err = io.ReadAll(io.LimitReader(part, blockpageMaxBytes+1))
					if err != nil {
						jsonErr(w, http.StatusBadRequest, "read file: "+err.Error())
						return
					}
					break
				}
			}
		default:
			raw, err := io.ReadAll(io.LimitReader(r.Body, blockpageMaxBytes+1024))
			if err != nil {
				jsonErr(w, http.StatusBadRequest, "read body: "+err.Error())
				return
			}
			if len(raw) > blockpageMaxBytes {
				jsonErr(w, http.StatusRequestEntityTooLarge, "page exceeds 1 MiB limit")
				return
			}
			var body struct {
				HTML string `json:"html"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				jsonErr(w, http.StatusBadRequest, "invalid json")
				return
			}
			data = []byte(body.HTML)
		}
		if len(data) > blockpageMaxBytes {
			jsonErr(w, http.StatusRequestEntityTooLarge, "page exceeds 1 MiB limit")
			return
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			jsonErr(w, http.StatusBadRequest, "empty page")
			return
		}
		if err := writeBlockpage(data); err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot save page: "+err.Error())
			return
		}
		jsonOK(w)
	case http.MethodDelete:
		if err := resetBlockpage(); err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot reset page: "+err.Error())
			return
		}
		jsonOK(w)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		jsonErr(w, http.StatusMethodNotAllowed, "GET, POST or DELETE only")
	}
}
