package main

import (
	"bufio"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/colinmarc/cdb"
	"github.com/miekg/dns"
)

// MasterState tracks master compilation status and history
type MasterState struct {
	mu            sync.Mutex
	IsBuilding    bool      `json:"is_building"`
	LastBuildTime time.Time `json:"last_build_time"`
	LastDuration  string    `json:"last_duration"`
	LastStatus    string    `json:"last_status"`
	LastError     string    `json:"last_error,omitempty"`
	LastSHA256    string    `json:"last_sha256,omitempty"`
	LastEntries   int       `json:"last_entries"`
	LastSizeMB    float64   `json:"last_size_mb"`
	BuildLogs     []string  `json:"build_logs"`
}

var masterState MasterState

var localDBDir = "/var/lib/dnsdist"

func cleanupVersionedDBs(dir, prefix, active string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == filepath.Base(active) || !strings.HasPrefix(entry.Name(), prefix+".") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), prefix+"."), ".db")
		if len(name) != 64 || strings.Trim(name, "0123456789abcdef") != "" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func appendMasterLog(msg string) {
	masterState.mu.Lock()
	defer masterState.mu.Unlock()
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	masterState.BuildLogs = append(masterState.BuildLogs, entry)
	if len(masterState.BuildLogs) > 100 {
		masterState.BuildLogs = masterState.BuildLogs[len(masterState.BuildLogs)-100:]
	}
	log.Printf("[master-builder] %s", msg)
}

type ChunkResult struct {
	index int
	data  []byte
	err   error
}

func md5CacheKey(url string) string {
	h := md5.Sum([]byte(url))
	return fmt.Sprintf("%x", h)
}

// downloadSourceToFile downloads a remote blacklist URL to a local cache file with ETag caching
func downloadSourceToFile(url, destPath string, connCount int, client *http.Client, force bool) (string, error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return "", fmt.Errorf("request HEAD gagal: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("koneksi HEAD gagal: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("URL mengembalikan status 404 Not Found")
	}

	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")
	contentLength := resp.ContentLength

	etagPath := destPath + ".meta"
	cacheExists := false

	if !force {
		if meta, err := os.ReadFile(etagPath); err == nil {
			parts := strings.SplitN(string(meta), "\n", 3)
			cachedEtag, cachedMod := "", ""
			if len(parts) >= 1 {
				cachedEtag = strings.TrimSpace(parts[0])
			}
			if len(parts) >= 2 {
				cachedMod = strings.TrimSpace(parts[1])
			}
			etagMatch := etag != "" && cachedEtag == etag
			modMatch := lastMod != "" && cachedMod == lastMod
			if etagMatch || modMatch {
				if _, statErr := os.Stat(destPath); statErr == nil {
					cacheExists = true
				}
			}
		}
	}

	if cacheExists {
		appendMasterLog(fmt.Sprintf("Cache valid (ETag/Date cocok): %s", url))
		return destPath, nil
	}

	dlStart := time.Now()
	tmpPath := destPath + ".downloading"
	_ = os.Remove(tmpPath)

	if contentLength <= 0 {
		appendMasterLog(fmt.Sprintf("Download single-stream: %s", url))
		reqGet, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}
		res, err := client.Do(reqGet)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()

		f, err := os.Create(tmpPath)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, res.Body); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return "", err
		}
		f.Close()
	} else {
		appendMasterLog(fmt.Sprintf("Download multi-part (%d koneksi, %d MB): %s", connCount, contentLength/(1024*1024), url))
		chunkSize := contentLength / int64(connCount)
		results := make([]ChunkResult, connCount)
		var wg sync.WaitGroup

		for i := 0; i < connCount; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				startByte := int64(idx) * chunkSize
				endByte := startByte + chunkSize - 1
				if idx == connCount-1 {
					endByte = contentLength - 1
				}

				r, _ := http.NewRequest("GET", url, nil)
				r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, endByte))

				res, err := client.Do(r)
				if err != nil {
					results[idx] = ChunkResult{index: idx, err: err}
					return
				}
				defer res.Body.Close()

				data, err := io.ReadAll(res.Body)
				results[idx] = ChunkResult{index: idx, data: data, err: err}
			}(i)
		}
		wg.Wait()

		f, err := os.Create(tmpPath)
		if err != nil {
			return "", err
		}
		for i := 0; i < connCount; i++ {
			if results[i].err != nil {
				f.Close()
				os.Remove(tmpPath)
				return "", fmt.Errorf("chunk %d gagal: %v", i, results[i].err)
			}
			if _, err := f.Write(results[i].data); err != nil {
				f.Close()
				os.Remove(tmpPath)
				return "", err
			}
			results[i].data = nil
		}
		f.Close()
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	_ = os.WriteFile(etagPath, []byte(fmt.Sprintf("%s\n%s\n", etag, lastMod)), 0644)
	appendMasterLog(fmt.Sprintf("Download selesai dalam %v: %s", time.Since(dlStart).Round(time.Millisecond), url))
	return destPath, nil
}

// loadWhitelistSet membaca whitelist file ke dalam map set
func loadWhitelistSet(path string) (map[string]struct{}, error) {
	wl := make(map[string]struct{})
	if path == "" {
		return wl, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return wl, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.ToLower(sc.Text()))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSuffix(line, ".")
		wl[line] = struct{}{}
	}
	return wl, sc.Err()
}

// BuildMasterCDB orchestrates downloading, parsing, compiling, hashing, and writing manifest
func BuildMasterCDB(outputDir, sourcesFile, whitelistFile, customBLFile string, connCount int, force bool) error {
	masterState.mu.Lock()
	if masterState.IsBuilding {
		masterState.mu.Unlock()
		return fmt.Errorf("kompilasi sedang berjalan")
	}
	masterState.IsBuilding = true
	masterState.mu.Unlock()

	defer func() {
		masterState.mu.Lock()
		masterState.IsBuilding = false
		masterState.mu.Unlock()
	}()

	start := time.Now()
	appendMasterLog("Memulai proses kompilasi CDB master...")

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("gagal membuat output dir: %w", err)
	}

	cacheDir := filepath.Join(outputDir, ".cache")
	_ = os.MkdirAll(cacheDir, 0755)

	whitelist, err := loadWhitelistSet(whitelistFile)
	if err != nil {
		appendMasterLog(fmt.Sprintf("Peringatan whitelist: %v", err))
	} else if len(whitelist) > 0 {
		appendMasterLog(fmt.Sprintf("Whitelist aktif: %d entri dari %s", len(whitelist), whitelistFile))
	}

	var sourceURLs []string
	if sourcesFile != "" {
		if f, err := os.Open(sourcesFile); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				sourceURLs = append(sourceURLs, line)
			}
			f.Close()
		}
	}

	if connCount <= 0 {
		connCount = 8
	}

	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: connCount + 2,
	}
	client := &http.Client{Transport: tr, Timeout: 180 * time.Second}

	var sourceFiles []string
	for _, rawURL := range sourceURLs {
		cacheKey := md5CacheKey(rawURL)
		destPath := filepath.Join(cacheDir, cacheKey+".txt")
		fpath, err := downloadSourceToFile(rawURL, destPath, connCount, client, force)
		if err != nil {
			appendMasterLog(fmt.Sprintf("Source GAGAL (%s): %v. Dilewati.", rawURL, err))
			continue
		}
		sourceFiles = append(sourceFiles, fpath)
	}

	if customBLFile != "" {
		if _, err := os.Stat(customBLFile); err == nil {
			sourceFiles = append(sourceFiles, customBLFile)
			appendMasterLog(fmt.Sprintf("Menyertakan blacklist lokal: %s", customBLFile))
		}
	}

	if len(sourceFiles) == 0 {
		err := fmt.Errorf("tidak ada sumber blacklist yang valid untuk dikompilasi")
		masterState.mu.Lock()
		masterState.LastStatus = "error"
		masterState.LastError = err.Error()
		masterState.mu.Unlock()
		return err
	}

	tmpCDB := filepath.Join(outputDir, "trust_building.tmp")
	_ = os.Remove(tmpCDB)

	writer, err := cdb.Create(tmpCDB)
	if err != nil {
		return fmt.Errorf("gagal membuat temporary CDB: %w", err)
	}

	countDomain := 0
	countIP := 0
	countWhitelisted := 0
	emptyVal := []byte{}
	wireKeyBuffer := make([]byte, 256)

	appendMasterLog(fmt.Sprintf("Menulis data CDB dari %d sumber...", len(sourceFiles)))

	for _, srcPath := range sourceFiles {
		f, err := os.Open(srcPath)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)

		for sc.Scan() {
			line := strings.TrimSpace(strings.ToLower(sc.Text()))
			if idx := strings.Index(line, "#"); idx != -1 {
				line = strings.TrimSpace(line[:idx])
			}
			if idx := strings.Index(line, "!"); idx != -1 {
				line = strings.TrimSpace(line[:idx])
			}
			if line == "" || strings.HasPrefix(line, ";") {
				continue
			}

			// Hosts format: 0.0.0.0 domain.com
			if strings.HasPrefix(line, "0.0.0.0 ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "0.0.0.0 "))
			} else if strings.HasPrefix(line, "127.0.0.1 ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "127.0.0.1 "))
			}

			// AdGuard format: ||domain.com^
			if strings.HasPrefix(line, "@@||") {
				continue
			}
			if strings.HasPrefix(line, "||") {
				line = strings.TrimPrefix(line, "||")
			}
			line = strings.TrimSuffix(line, "^")

			// Dnsmasq format: address=/domain.com/...
			if strings.HasPrefix(line, "address=/") {
				parts := strings.Split(line, "/")
				if len(parts) >= 3 {
					line = parts[1]
				}
			}

			if strings.Contains(line, "/") || strings.Contains(line, "*") || line == "" {
				continue
			}

			var wireKey []byte
			if ip := net.ParseIP(line); ip != nil {
				if _, ok := whitelist[line]; ok {
					countWhitelisted++
					continue
				}
				wireKey = append([]byte(line), 0)
				countIP++
			} else {
				domainPlain := strings.TrimSuffix(line, ".")
				if _, ok := whitelist[domainPlain]; ok {
					countWhitelisted++
					continue
				}
				domain := domainPlain + "."
				off, err := dns.PackDomainName(domain, wireKeyBuffer, 0, nil, false)
				if err != nil {
					continue
				}
				wireKey = wireKeyBuffer[:off]
				countDomain++
			}

			if err := writer.Put(wireKey, emptyVal); err != nil {
				writer.Close()
				os.Remove(tmpCDB)
				f.Close()
				return fmt.Errorf("gagal menulis ke CDB: %w", err)
			}
		}
		f.Close()
	}

	if err := writer.Close(); err != nil {
		os.Remove(tmpCDB)
		return fmt.Errorf("gagal menutup file CDB: %w", err)
	}

	fi, err := os.Stat(tmpCDB)
	if err != nil || fi.Size() < 2048 {
		os.Remove(tmpCDB)
		return fmt.Errorf("ukuran CDB tidak valid (< 2KB)")
	}

	// Calculate SHA256
	h := sha256.New()
	cf, err := os.Open(tmpCDB)
	if err != nil {
		return err
	}
	_, _ = io.Copy(h, cf)
	cf.Close()
	shaStr := fmt.Sprintf("%x", h.Sum(nil))

	finalHashed := filepath.Join(outputDir, fmt.Sprintf("trust.%s.db", shaStr))
	finalLink := filepath.Join(outputDir, "trust.db")
	manifestPath := filepath.Join(outputDir, "manifest.json")

	_ = os.Rename(tmpCDB, finalHashed)
	_ = os.Chmod(finalHashed, 0644)

	// Atomic symlink
	tmpLink := finalLink + ".tmp"
	_ = os.Remove(tmpLink)
	_ = os.Symlink(filepath.Base(finalHashed), tmpLink)
	_ = os.Rename(tmpLink, finalLink)

	// Retain only the active content-addressed DB after the atomic swap.
	if removed, err := cleanupVersionedDBs(outputDir, "trust", finalHashed); err != nil {
		appendMasterLog(fmt.Sprintf("Peringatan cleanup DB lama: %v", err))
	} else if removed > 0 {
		appendMasterLog(fmt.Sprintf("Cleanup DB lama: %d file", removed))
	}
	versionCount := 1

	builtAt := time.Now().UTC().Format(time.RFC3339)
	manifestData := fmt.Sprintf(`{
  "version": %d,
  "sha256": "%s",
  "size": %d,
  "built_at": "%s",
  "download_url": "/files/trust.db",
  "entries": %d
}
`, versionCount, shaStr, fi.Size(), builtAt, countDomain+countIP)

	_ = atomicWriteString(manifestPath, manifestData, 0644)

	// Update local node if exists
	if _, err := os.Stat(localDBDir); err == nil {
		_ = os.Symlink(finalHashed, filepath.Join(localDBDir, "blacklist.db.tmp"))
		_ = os.Rename(filepath.Join(localDBDir, "blacklist.db.tmp"), filepath.Join(localDBDir, "blacklist.db"))
	}

	duration := time.Since(start).Round(time.Millisecond)
	total := countDomain + countIP
	sizeMB := float64(fi.Size()) / (1024 * 1024)

	masterState.mu.Lock()
	masterState.LastBuildTime = time.Now()
	masterState.LastDuration = duration.String()
	masterState.LastStatus = "success"
	masterState.LastError = ""
	masterState.LastSHA256 = shaStr
	masterState.LastEntries = total
	masterState.LastSizeMB = math.Round(sizeMB*10) / 10
	masterState.mu.Unlock()

	appendMasterLog(fmt.Sprintf("Kompilasi SELESAI! %d entri (%d Domain, %d IP, %d whitelisted) -> %.1f MB dalam %v",
		total, countDomain, countIP, countWhitelisted, sizeMB, duration))

	return nil
}
