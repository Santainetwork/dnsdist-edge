package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Path file yang dibaca dnsdist (lihat setup/dnsdist.conf blok [7.1]).
const localBlockPath = "/etc/dnsdist/local-block.txt"

var localBlock = newLocalBlockStore(envOr("PANEL_LOCAL_BLOCK_FILE", localBlockPath))

// reloadLocalBlock menerapkan perubahan: dnsdist hanya membaca config saat start.
// Gagal restart -> 500 agar UI tahu perubahan belum aktif.
func reloadLocalBlock(w http.ResponseWriter) {
	if err := restartDnsdist(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "daftar tersimpan, tetapi restart dnsdist gagal: "+err.Error())
		return
	}
	jsonOK(w)
}

// handleLocalBlock: GET daftar, POST {"domains":[...]} tambah, DELETE ?domain=x hapus.
// Di belakang auth() (lihat main.go).
func handleLocalBlock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"domains": localBlock.List()})
	case http.MethodPost:
		var body struct {
			Domains []string `json:"domains"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || len(body.Domains) == 0 {
			jsonErr(w, http.StatusBadRequest, "domains wajib")
			return
		}
		if err := localBlock.Add(body.Domains); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), errLocalBlockInvalid.Error()) {
				status = http.StatusBadRequest
			}
			jsonErr(w, status, err.Error())
			return
		}
		reloadLocalBlock(w)
	case http.MethodDelete:
		d := r.URL.Query().Get("domain")
		if strings.TrimSpace(d) == "" {
			jsonErr(w, http.StatusBadRequest, "domain wajib")
			return
		}
		if err := localBlock.Remove(d); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		reloadLocalBlock(w)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET, POST, or DELETE only")
	}
}
