package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// masterBuildFn memicu kompilasi CDB di background, seperti handleMasterBuild.
// Dibungkus agar tes tidak menjalankan kompilasi sungguhan.
var masterBuildFn = func() {
	go func() {
		_ = BuildMasterCDB(*flagFilesDir, *flagSourcesFile, *flagWhitelistFile, *flagCustomBLFile, 8, true)
	}()
}

// handleMasterCustomBlacklist: jalur 2 (central). GET daftar, POST
// {"domains":[...]} menggantikan daftar. Ditulis ke flagCustomBLFile, yang
// dikompilasi BuildMasterCDB ke CDB dan didistribusikan ke edge.
// Mode rpz-slave tidak boleh menulis (sama seperti whitelist).
func handleMasterCustomBlacklist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := *flagCustomBLFile
	switch r.Method {
	case http.MethodGet:
		content, _ := os.ReadFile(path)
		var domains []string
		for _, line := range strings.Split(string(content), "\n") {
			if d := strings.TrimSpace(line); d != "" {
				domains = append(domains, d)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"domains": domains, "count": len(domains)})
	case http.MethodPost:
		if *flagSourceMode == "rpz-slave" {
			jsonErr(w, http.StatusConflict, "custom blacklist writes disabled in rpz-slave source mode")
			return
		}
		var body struct {
			Domains []string `json:"domains"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		set := make(map[string]struct{}, len(body.Domains))
		for _, raw := range body.Domains {
			d, err := normalizeLocalBlockDomain(raw)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid domain: %q", raw))
				return
			}
			set[d] = struct{}{}
		}
		keys := make([]string, 0, len(set))
		for d := range set {
			keys = append(keys, d)
		}
		sort.Strings(keys)
		content := strings.Join(keys, "\n")
		if content != "" {
			content += "\n"
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot create directory: "+err.Error())
			return
		}
		if err := atomicWriteString(path, content, 0o644); err != nil {
			jsonErr(w, http.StatusInternalServerError, "cannot save custom blacklist: "+err.Error())
			return
		}
		masterBuildFn()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "count": len(keys), "build": "started"})
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}
