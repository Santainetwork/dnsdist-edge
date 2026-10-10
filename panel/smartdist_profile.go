package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Profil SmartDist: dokumen JSON yang disimpan panel dan ditunjuk ke node
// lewat heartbeat. Hash = sha256 hex dari bytes JSON canonical (json.Marshal
// dari struct, bukan map), sehingga hash stabil terhadap urutan key di input.

var smartDistIDRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

var (
	errSmartDistNotFound = errors.New("profil tidak ditemukan")
	errSmartDistInUse    = errors.New("profil masih di-assign ke node")
	errSmartDistStorage  = errors.New("gagal menyimpan profil")
)

type SmartDistProfile struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Smartdist SmartDistSettings `json:"smartdist"`
}

type SmartDistSettings struct {
	Enabled bool                `json:"enabled"`
	Rules   []SmartDistRuleSpec `json:"rules"`
}

// SmartDistRuleSpec memuat semua field rule; field yang tak relevan dengan
// type dihilangkan dari JSON canonical oleh omitempty.
type SmartDistRuleSpec struct {
	Type    string   `json:"type"`
	Name    string   `json:"name,omitempty"`
	File    string   `json:"file,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Target  string   `json:"target,omitempty"`
	IPSet   string   `json:"ip_set,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Exclude string   `json:"exclude,omitempty"`
}

// parseSmartDistProfile memvalidasi input dan mengembalikan bytes canonical.
func parseSmartDistProfile(raw []byte) (SmartDistProfile, []byte, error) {
	var p SmartDistProfile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, nil, fmt.Errorf("profil JSON tidak valid: %w", err)
	}
	if !smartDistIDRe.MatchString(p.ID) {
		return p, nil, errors.New("id harus cocok [a-z0-9-]{1,40}")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 200 {
		return p, nil, errors.New("name wajib diisi, maksimal 200 byte")
	}
	if p.Smartdist.Rules == nil {
		p.Smartdist.Rules = []SmartDistRuleSpec{}
	}
	for i, r := range p.Smartdist.Rules {
		var ok bool
		switch r.Type {
		case "ip_set":
			ok = r.Name != "" && r.File != ""
		case "cname":
			ok = r.Pattern != "" && r.Target != ""
		case "alias":
			ok = r.IPSet != "" && len(r.Targets) > 0
		}
		if !ok {
			return p, nil, fmt.Errorf("rule ke-%d: type %q tidak dikenal atau field wajib kosong", i, r.Type)
		}
	}
	canon, err := json.Marshal(p)
	if err != nil {
		return p, nil, err
	}
	return p, canon, nil
}

func smartDistCanonHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ─── Store ───────────────────────────────────────────────────────────────────

type smartDistEntry struct {
	raw  []byte
	hash string
}

type SmartDistProfileSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Rules   int    `json:"rules"`
	Hash    string `json:"hash"`
}

// SmartDistProfileStore menyimpan profil dan assignment node->profil.
// Pola persistensi sama dengan ClusterStore: JSON file, tulis atomik.
type SmartDistProfileStore struct {
	mu       sync.RWMutex
	filePath string
	profiles map[string]smartDistEntry
	assign   map[string]string
}

type smartDistDiskFormat struct {
	Profiles map[string]json.RawMessage `json:"profiles"`
	Assign   map[string]string          `json:"assign"`
}

func newSmartDistProfileStore(filePath string) *SmartDistProfileStore {
	s := &SmartDistProfileStore{
		filePath: filePath,
		profiles: make(map[string]smartDistEntry),
		assign:   make(map[string]string),
	}
	s.load()
	return s
}

func (s *SmartDistProfileStore) load() {
	if s.filePath == "" {
		return
	}
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	var d smartDistDiskFormat
	if err := json.Unmarshal(b, &d); err != nil {
		log.Printf("[smartdist] file profil rusak, diabaikan: %v", err)
		return
	}
	for id, raw := range d.Profiles {
		p, canon, err := parseSmartDistProfile(raw)
		if err != nil || p.ID != id {
			log.Printf("[smartdist] profil %q dilewati saat load: tidak valid", id)
			continue
		}
		s.profiles[id] = smartDistEntry{raw: canon, hash: smartDistCanonHash(canon)}
	}
	for node, pid := range d.Assign {
		if _, ok := s.profiles[pid]; ok {
			s.assign[node] = pid
		}
	}
}

// saveLocked harus dipanggil dengan s.mu terkunci.
func (s *SmartDistProfileStore) saveLocked() error {
	if s.filePath == "" {
		return nil
	}
	d := smartDistDiskFormat{
		Profiles: make(map[string]json.RawMessage, len(s.profiles)),
		Assign:   s.assign,
	}
	for id, e := range s.profiles {
		d.Profiles[id] = e.raw
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(s.filePath), 0o755)
	return atomicWrite(s.filePath, b, 0o600)
}

// Put memvalidasi dan menyimpan profil (create atau replace).
func (s *SmartDistProfileStore) Put(raw []byte) (SmartDistProfileSummary, error) {
	p, canon, err := parseSmartDistProfile(raw)
	if err != nil {
		return SmartDistProfileSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.profiles[p.ID]
	s.profiles[p.ID] = smartDistEntry{raw: canon, hash: smartDistCanonHash(canon)}
	if err := s.saveLocked(); err != nil {
		if had {
			s.profiles[p.ID] = prev
		} else {
			delete(s.profiles, p.ID)
		}
		return SmartDistProfileSummary{}, fmt.Errorf("%w: %v", errSmartDistStorage, err)
	}
	return summarize(p, s.profiles[p.ID].hash), nil
}

func summarize(p SmartDistProfile, hash string) SmartDistProfileSummary {
	return SmartDistProfileSummary{
		ID: p.ID, Name: p.Name, Enabled: p.Smartdist.Enabled,
		Rules: len(p.Smartdist.Rules), Hash: hash,
	}
}

// Get mengembalikan bytes canonical yang persis di-serve dan hash-nya.
func (s *SmartDistProfileStore) Get(id string) ([]byte, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.profiles[id]
	if !ok {
		return nil, "", false
	}
	return e.raw, e.hash, true
}

func (s *SmartDistProfileStore) List() []SmartDistProfileSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SmartDistProfileSummary, 0, len(s.profiles))
	for _, e := range s.profiles {
		var p SmartDistProfile
		_ = json.Unmarshal(e.raw, &p)
		out = append(out, summarize(p, e.hash))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Delete menolak penghapusan profil yang masih di-assign.
func (s *SmartDistProfileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.profiles[id]
	if !ok {
		return errSmartDistNotFound
	}
	for _, pid := range s.assign {
		if pid == id {
			return errSmartDistInUse
		}
	}
	delete(s.profiles, id)
	if err := s.saveLocked(); err != nil {
		s.profiles[id] = e
		return fmt.Errorf("%w: %v", errSmartDistStorage, err)
	}
	return nil
}

// Assign mengikat node ke profil. profileID "" = lepas assignment.
func (s *SmartDistProfileStore) Assign(nodeID, profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.assign[nodeID]
	if profileID == "" {
		delete(s.assign, nodeID)
	} else {
		if _, ok := s.profiles[profileID]; !ok {
			return errSmartDistNotFound
		}
		s.assign[nodeID] = profileID
	}
	if err := s.saveLocked(); err != nil {
		if had {
			s.assign[nodeID] = prev
		} else {
			delete(s.assign, nodeID)
		}
		return fmt.Errorf("%w: %v", errSmartDistStorage, err)
	}
	return nil
}

// AssignMany menugaskan profil ke banyak node secara atomik: semua node dan
// profil divalidasi dulu, baru satu kali tulis. Gagal -> tidak ada yang berubah.
func (s *SmartDistProfileStore) AssignMany(nodeIDs []string, profileID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[profileID]; !ok {
		return errSmartDistNotFound
	}
	prev := make(map[string]string, len(nodeIDs))
	for _, id := range nodeIDs {
		if v, had := s.assign[id]; had {
			prev[id] = v
		} else {
			prev[id] = "\x00"
		}
	}
	for _, id := range nodeIDs {
		s.assign[id] = profileID
	}
	if err := s.saveLocked(); err != nil {
		for id, v := range prev {
			if v == "\x00" {
				delete(s.assign, id)
			} else {
				s.assign[id] = v
			}
		}
		return fmt.Errorf("%w: %v", errSmartDistStorage, err)
	}
	return nil
}

// ProfileForNode mengembalikan id dan hash profil yang di-assign ke node.
func (s *SmartDistProfileStore) ProfileForNode(nodeID string) (string, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pid, ok := s.assign[nodeID]
	if !ok {
		return "", "", false
	}
	e, ok := s.profiles[pid]
	if !ok {
		return "", "", false
	}
	return pid, e.hash, true
}

// ─── Global instance ─────────────────────────────────────────────────────────

var (
	smartDistStoreMu   sync.Mutex
	smartDistStoreInst *SmartDistProfileStore
)

// getSmartDistStore memuat store sekali, lokasi file berdampingan dengan
// file nodes cluster. Backend URL/SQL tidak punya path file, pakai default.
func getSmartDistStore() *SmartDistProfileStore {
	smartDistStoreMu.Lock()
	defer smartDistStoreMu.Unlock()
	if smartDistStoreInst == nil {
		base := "/var/lib/dnsdist/cluster-nodes.json"
		if flagClusterNodesFile != nil && *flagClusterNodesFile != "" && !strings.Contains(*flagClusterNodesFile, "://") {
			base = *flagClusterNodesFile
		}
		smartDistStoreInst = newSmartDistProfileStore(filepath.Join(filepath.Dir(base), "smartdist-profiles.json"))
	}
	return smartDistStoreInst
}

func setSmartDistStore(s *SmartDistProfileStore) {
	smartDistStoreMu.Lock()
	defer smartDistStoreMu.Unlock()
	smartDistStoreInst = s
}

// ─── Node authentication ─────────────────────────────────────────────────────

// VerifyNodeKey mencocokkan node_id + node_key dengan NodeRecord (konstan waktu).
func (cs *ClusterStore) VerifyNodeKey(nodeID, key string) bool {
	if cs == nil || nodeID == "" || key == "" {
		return false
	}
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	rec, ok := cs.nodes[nodeID]
	if !ok || rec.Key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rec.Key), []byte(key)) == 1
}

type nodeKeyVerifier interface {
	VerifyNodeKey(nodeID, key string) bool
}

// verifyEdgeNode fail-closed: backend tanpa VerifyNodeKey selalu ditolak.
func verifyEdgeNode(nodeID, key string) bool {
	if nodeID == "" || key == "" {
		return false
	}
	v, ok := getClusterStorage().(nodeKeyVerifier)
	return ok && v.VerifyNodeKey(nodeID, key)
}

// ─── HTTP handlers ───────────────────────────────────────────────────────────

// handleClusterProfileFetch: GET /api/cluster/profile?node_id=&node_key=
// Publik (tanpa JWT), autentikasi node. 401 bila node/key salah, 404 bila
// tidak ada profil yang di-assign.
func handleClusterProfileFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	q := r.URL.Query()
	nodeID := strings.TrimSpace(q.Get("node_id"))
	nodeKey := strings.TrimSpace(q.Get("node_key"))
	if !verifyEdgeNode(nodeID, nodeKey) {
		jsonErr(w, http.StatusUnauthorized, "node tidak terautentikasi")
		return
	}
	st := getSmartDistStore()
	pid, _, ok := st.ProfileForNode(nodeID)
	if !ok {
		jsonErr(w, http.StatusNotFound, "tidak ada profil untuk node ini")
		return
	}
	raw, _, ok := st.Get(pid)
	if !ok {
		jsonErr(w, http.StatusNotFound, "tidak ada profil untuk node ini")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// handleSmartDistProfiles: admin (dibungkus auth() di main.go).
// GET = daftar, POST = create/replace (body JSON profil), DELETE ?id=.
func handleSmartDistProfiles(w http.ResponseWriter, r *http.Request) {
	st := getSmartDistStore()
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"profiles": st.List()})
	case http.MethodPost:
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "body terlalu besar atau tidak terbaca")
			return
		}
		sum, err := st.Put(raw)
		if err != nil {
			if errors.Is(err, errSmartDistStorage) {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sum)
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			jsonErr(w, http.StatusBadRequest, "id parameter required")
			return
		}
		switch err := st.Delete(id); {
		case err == nil:
			jsonOK(w)
		case errors.Is(err, errSmartDistNotFound):
			jsonErr(w, http.StatusNotFound, err.Error())
		case errors.Is(err, errSmartDistInUse):
			jsonErr(w, http.StatusConflict, err.Error())
		default:
			jsonErr(w, http.StatusInternalServerError, err.Error())
		}
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "GET, POST, or DELETE only")
	}
}

// handleSmartDistAssign: admin. POST {"node_id":"...","profile_id":"..."}.
// profile_id kosong = lepas assignment.
func handleSmartDistAssign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		NodeID    string   `json:"node_id"`
		NodeIDs   []string `json:"node_ids"`
		ProfileID string   `json:"profile_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid json payload")
		return
	}
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	ids := req.NodeIDs
	if len(ids) == 0 && strings.TrimSpace(req.NodeID) != "" {
		ids = []string{req.NodeID}
	}
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
	}
	if len(ids) == 0 || ids[0] == "" {
		jsonErr(w, http.StatusBadRequest, "node_id wajib")
		return
	}
	nodes, _ := getClusterStorage().ListNodes()
	known := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		known[n.ID] = true
	}
	for _, id := range ids {
		if id == "" || !known[id] {
			jsonErr(w, http.StatusNotFound, "node tidak ditemukan: "+id)
			return
		}
	}
	if err := getSmartDistStore().AssignMany(ids, req.ProfileID); err != nil {
		if errors.Is(err, errSmartDistNotFound) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"assigned": ids})
}
