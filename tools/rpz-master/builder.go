package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/colinmarc/cdb"
	"github.com/miekg/dns"
)

type BuildResult struct {
	TotalEntries int
	SHA256       string
	Duration     time.Duration
	CDBPath      string
}

// DownloadDomainsToFile streams an HTTP source to a temporary file and publishes it atomically.
func DownloadDomainsToFile(sourceURL string, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(destPath), ".domains-*")
	if err != nil {
		return err
	}
	tmpPath := out.Name()
	defer os.Remove(tmpPath)

	request, err := http.NewRequest(http.MethodGet, sourceURL, nil)
	if err != nil {
		_ = out.Close()
		return err
	}
	request.Header.Set("User-Agent", "SantaiNetwork-RPZ-Sync/1.0")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		_ = out.Close()
		return fmt.Errorf("gagal download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = out.Close()
		return fmt.Errorf("server HTTP mengembalikan status: %s", response.Status)
	}
	if _, err := io.Copy(out, response.Body); err != nil {
		_ = out.Close()
		return fmt.Errorf("gagal menulis body: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, destPath)
}

// CompileDomainsToCDB compiles a line-delimited domain file into a CDB file.
// A scanner or writer error leaves the existing destination untouched.
func CompileDomainsToCDB(rawFile string, cdbPath string, wireFormat bool) (*BuildResult, error) {
	start := time.Now()
	input, err := os.Open(rawFile)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka raw domain file: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(cdbPath), 0o755); err != nil {
		return nil, err
	}
	out, err := os.CreateTemp(filepath.Dir(cdbPath), ".cdb-*")
	if err != nil {
		return nil, err
	}
	tmpCDB := out.Name()
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpCDB)
		return nil, err
	}
	defer os.Remove(tmpCDB)

	writer, err := cdb.Create(tmpCDB)
	if err != nil {
		return nil, fmt.Errorf("gagal inisialisasi cdb writer: %w", err)
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	wireBuf := make([]byte, 256)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domain, err := normalizePolicyDomain(line)
		if err != nil {
			_ = writer.Close()
			return nil, err
		}
		key := []byte(domain + ".")
		if wireFormat {
			off, err := dns.PackDomainName(domain+".", wireBuf, 0, nil, false)
			if err != nil {
				_ = writer.Close()
				return nil, err
			}
			key = append([]byte(nil), wireBuf[:off]...)
		}
		if err := writer.Put(key, nil); err != nil {
			_ = writer.Close()
			return nil, fmt.Errorf("gagal write key ke CDB: %w", err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("gagal membaca raw domain file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("gagal freeze cdb: %w", err)
	}

	built, err := os.Open(tmpCDB)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, built); err != nil {
		_ = built.Close()
		return nil, err
	}
	if err := built.Close(); err != nil {
		return nil, err
	}
	hashStr := hex.EncodeToString(hash.Sum(nil))
	if err := os.Chmod(tmpCDB, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpCDB, cdbPath); err != nil {
		return nil, fmt.Errorf("atomic rename gagal: %w", err)
	}
	if err := writeManifest(cdbPath, hashStr, count); err != nil {
		return nil, err
	}
	return &BuildResult{TotalEntries: count, SHA256: hashStr, Duration: time.Since(start), CDBPath: cdbPath}, nil
}

func writeManifest(cdbPath, hash string, count int) error {
	manifest := map[string]any{
		"sha256":        hash,
		"total_entries": count,
		"built_at":      time.Now().UTC().Format(time.RFC3339),
		"cdb_file":      filepath.Base(cdbPath),
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	manifestPath := filepath.Join(filepath.Dir(cdbPath), "manifest.json")
	out, err := os.CreateTemp(filepath.Dir(cdbPath), ".manifest-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, manifestPath)
}

// PublishDelta stages raw and CDB files completely before atomically replacing
// their destinations. Validation and compile failures leave current files intact.
func PublishDelta(cfg Config, delta *Delta) (*BuildResult, error) {
	if delta == nil {
		return nil, errors.New("delta is required")
	}
	if info, err := os.Stat(cfg.CDBPath); err == nil && info.IsDir() {
		return nil, errors.New("cdb_path must not be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if info, err := os.Stat(cfg.RawDomainFile); err == nil && info.IsDir() {
		return nil, errors.New("raw_domain_file must not be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	rawDir := filepath.Dir(cfg.RawDomainFile)
	if err := os.MkdirAll(rawDir, 0o700); err != nil {
		return nil, err
	}
	stagedRaw, err := os.CreateTemp(rawDir, ".domains-stage-*")
	if err != nil {
		return nil, err
	}
	stagedRawPath := stagedRaw.Name()
	defer os.Remove(stagedRawPath)
	if !delta.Full {
		current, err := os.Open(cfg.RawDomainFile)
		if err != nil {
			_ = stagedRaw.Close()
			return nil, err
		}
		if _, err := io.Copy(stagedRaw, current); err != nil {
			_ = current.Close()
			_ = stagedRaw.Close()
			return nil, err
		}
		if err := current.Close(); err != nil {
			_ = stagedRaw.Close()
			return nil, err
		}
	}
	if err := stagedRaw.Close(); err != nil {
		return nil, err
	}
	if err := ApplyDeltaToFile(stagedRawPath, delta); err != nil {
		return nil, err
	}

	cdbDir := filepath.Dir(cfg.CDBPath)
	if err := os.MkdirAll(cdbDir, 0o755); err != nil {
		return nil, err
	}
	stageDir, err := os.MkdirTemp(cdbDir, ".publish-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stageDir)
	stagedCDB := filepath.Join(stageDir, filepath.Base(cfg.CDBPath))
	result, err := CompileDomainsToCDB(stagedRawPath, stagedCDB, true)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(stagedRawPath, cfg.RawDomainFile); err != nil {
		return nil, err
	}
	if err := os.Rename(stagedCDB, cfg.CDBPath); err != nil {
		return nil, err
	}
	if err := os.Rename(filepath.Join(stageDir, "manifest.json"), filepath.Join(cdbDir, "manifest.json")); err != nil {
		return nil, err
	}
	result.CDBPath = cfg.CDBPath
	return result, nil
}
