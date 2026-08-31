package main

import (
	"bufio"
	"crypto/md5"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
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

const cacheDir = "/tmp/trust-builder-cache"

func showHelp() {
	fmt.Println(`
  ████████╗██████╗ ██╗   ██╗███████╗████████╗    ██████╗ ██╗   ██╗██╗██╗     ██████╗ ███████╗██████╗ 
  ╚══██╔══╝██╔══██╗██║   ██║██╔════╝╚══██╔══╝    ██╔══██╗██║   ██║██║██║     ██╔══██╗██╔════╝██╔══██╗
     ██║   ██████╔╝██║   ██║███████╗   ██║       ██████╔╝██║   ██║██║██║     ██║  ██║█████╗  ██████╔╝
     ██║   ██╔══██╗██║   ██║╚════██║   ██║       ██╔══██╗██║   ██║██║██║     ██║  ██║██╔══╝  ██╔══██╗
     ██║   ██║  ██║╚██████╔╝███████║   ██║       ██████╔╝╚██████╔╝██║███████╗██████╔╝███████╗██║  ██║
     ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ╚══════╝   ╚═╝       ╚═════╝  ╚═════╝ ╚═╝╚══════╝╚═════╝ ╚══════╝╚═╝  ╚═╝

  High Performance CDB Blacklist Generator for DNSDist & Unbound
  Disk-Streaming | Multi-Part Downloader | ETag Cache | Atomic Replace

  Terinspirasi dari proyek Trust-NG (trust-ng-replica)

  Penggunaan:
    trust-builder [opsi] [file_input_1 file_input_2 ...]

  Opsi:
    -o  string  Lokasi output CDB (default "blacklist.db")
    -u  string  Satu atau lebih URL dipisah koma
    -w  string  File whitelist (domain/IP yang dikecualikan dari DB)
    -c  int     Jumlah koneksi paralel per URL (default 8)
    -force      Paksa re-download meski ETag sama
    -h          Tampilkan panduan ini

  Fitur Keamanan:
    - Download ke file cache disk, bukan RAM (hemat memori).
    - ETag/Last-Modified per-URL: skip re-download jika tidak berubah.
    - Source yang gagal dilewati tanpa merusak file output.
    - Atomic replace: file output hanya diganti jika seluruh build sukses.

  Contoh:
    trust-builder -u "https://trustpositif.komdigi.go.id/assets/db/domains_isp" -o blacklist.db
    trust-builder -u "url1,url2" -w whitelist.txt -o blacklist.db
    trust-builder -o blacklist.db domains.txt extra.txt
`)
	os.Exit(0)
}

// urlCacheKey membuat nama file cache unik berdasarkan URL (MD5 hash)
func urlCacheKey(url string) string {
	h := md5.Sum([]byte(url))
	return fmt.Sprintf("%x", h)
}

// Chunk untuk multi-part download
type Chunk struct {
	index int
	data  []byte
	err   error
}

// downloadToFile mengunduh URL menggunakan multi-part ke file di disk.
// Mengembalikan path file cache dan error. Memory usage = chunkSize * connCount saja.
func downloadToFile(url string, destPath string, connCount int, client *http.Client) (string, error) {
	// HEAD request untuk metadata
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return "", fmt.Errorf("gagal membuat request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi server: %w", err)
	}
	resp.Body.Close()

	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")
	contentLength := resp.ContentLength

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file tidak ditemukan (status: 404)")
	}

	// Tulis file ETag untuk URL ini
	etagPath := destPath + ".meta"
	cacheExists := false

	// Cek apakah cache sudah ada dan masih valid
	if meta, err := os.ReadFile(etagPath); err == nil {
		parts := strings.SplitN(string(meta), "\n", 3)
		cachedEtag, cachedMod := "", ""
		if len(parts) >= 1 {
			cachedEtag = strings.TrimSpace(parts[0])
		}
		if len(parts) >= 2 {
			cachedMod = strings.TrimSpace(parts[1])
		}

		// Cache valid jika ETag atau Last-Modified cocok
		etagMatch := etag != "" && cachedEtag == etag
		modMatch := lastMod != "" && cachedMod == lastMod

		if etagMatch || modMatch {
			if _, statErr := os.Stat(destPath); statErr == nil {
				cacheExists = true
			}
		}
	}

	if cacheExists {
		fmt.Printf("    [=] Cache valid (ETag cocok). Menggunakan file lokal.\n")
		return destPath, nil
	}

	dlStart := time.Now()
	tmpPath := destPath + ".downloading"

	if contentLength <= 0 {
		fmt.Printf("    [↓] Download single-thread (ukuran file tidak diketahui)\n")
		
		reqGet, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", fmt.Errorf("gagal membuat GET request: %w", err)
		}
		res, err := client.Do(reqGet)
		if err != nil {
			return "", fmt.Errorf("gagal download data: %w", err)
		}
		defer res.Body.Close()

		f, err := os.Create(tmpPath)
		if err != nil {
			return "", fmt.Errorf("gagal membuat file temp: %w", err)
		}
		
		if _, err := io.Copy(f, res.Body); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("gagal menulis streaming ke disk: %w", err)
		}
		f.Close()
	} else {
		fmt.Printf("    [↓] Download multi-part (%d koneksi) - %d bytes\n", connCount, contentLength)
		
		chunkSize := contentLength / int64(connCount)
		results := make([]Chunk, connCount)
		var wg sync.WaitGroup

		for i := 0; i < connCount; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				startByte := int64(index) * chunkSize
				endByte := startByte + chunkSize - 1
				if index == connCount-1 {
					endByte = contentLength - 1
				}

				r, _ := http.NewRequest("GET", url, nil)
				r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", startByte, endByte))

				res, err := client.Do(r)
				if err != nil {
					results[index] = Chunk{index: index, err: err}
					return
				}
				defer res.Body.Close()

				data, err := io.ReadAll(res.Body)
				results[index] = Chunk{index: index, data: data, err: err}
			}(i)
		}
		wg.Wait()

		f, err := os.Create(tmpPath)
		if err != nil {
			return "", fmt.Errorf("gagal membuat file temp: %w", err)
		}

		for i := 0; i < connCount; i++ {
			if results[i].err != nil {
				f.Close()
				os.Remove(tmpPath)
				return "", fmt.Errorf("chunk %d gagal: %w", i, results[i].err)
			}
			if _, err := f.Write(results[i].data); err != nil {
				f.Close()
				os.Remove(tmpPath)
				return "", fmt.Errorf("gagal menulis chunk %d ke disk: %w", i, err)
			}
			results[i].data = nil
		}
		f.Close()
	}

	// Atomic replace file cache
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("gagal finalisasi cache: %w", err)
	}

	// Simpan ETag + Last-Modified ke meta file
	metaContent := fmt.Sprintf("%s\n%s\n", etag, lastMod)
	os.WriteFile(etagPath, []byte(metaContent), 0644)

	fmt.Printf("    [✓] Selesai dalam %v\n", time.Since(dlStart))
	return destPath, nil
}

// loadWhitelist membaca file whitelist dan mengembalikan map untuk lookup O(1)
func loadWhitelist(path string) (map[string]struct{}, error) {
	wl := make(map[string]struct{})
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		domain := strings.TrimSpace(strings.ToLower(sc.Text()))
		if domain == "" || strings.HasPrefix(domain, "#") {
			continue
		}
		domain = strings.TrimSuffix(domain, ".")
		wl[domain] = struct{}{}
	}
	return wl, sc.Err()
}

func main() {
	helpFlag := flag.Bool("h", false, "Tampilkan bantuan")
	outputFile := flag.String("o", "blacklist.db", "File output database")
	urlsFlag := flag.String("u", "", "URL sumber (pisahkan dengan koma untuk multi-source)")
	whitelistFile := flag.String("w", "", "File whitelist domain/IP")
	connCount := flag.Int("c", 8, "Jumlah koneksi paralel per URL")
	
	var forceFlag bool
	flag.BoolVar(&forceFlag, "f", false, "Alias untuk -force")
	flag.BoolVar(&forceFlag, "force", false, "Paksa re-download meski cache masih valid")

	versionFlag := flag.Bool("v", false, "Tampilkan versi aplikasi")

	flag.Parse()

	if *versionFlag {
		fmt.Println("Trust-NG CDB Builder v1.1.0-native")
		os.Exit(0)
	}

	if *helpFlag {
		showHelp()
	}

	start := time.Now()

	// Pastikan folder cache tersedia
	os.MkdirAll(cacheDir, 0755)

	// HTTP client yang di-tune untuk kecepatan
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: *connCount + 2,
	}
	client := &http.Client{Transport: tr, Timeout: 120 * time.Second}

	// --- 1. Load Whitelist ---
	whitelist := make(map[string]struct{})
	if *whitelistFile != "" {
		wl, err := loadWhitelist(*whitelistFile)
		if err != nil {
			log.Fatalf("[!] Gagal membaca whitelist '%s': %v", *whitelistFile, err)
		}
		whitelist = wl
		fmt.Printf("[*] Whitelist: %d entri dari '%s'\n", len(whitelist), *whitelistFile)
	}

	// --- 2. Kumpulkan path file sumber ---
	var sourcePaths []string // path file di disk (bisa cache atau file lokal)
	successSources := 0
	skippedSources := 0

	// 2a. URL sources → download ke disk cache
	if *urlsFlag != "" {
		urls := strings.Split(*urlsFlag, ",")
		for _, rawURL := range urls {
			url := strings.TrimSpace(rawURL)
			if url == "" {
				continue
			}

			cacheKey := urlCacheKey(url)
			cachePath := filepath.Join(cacheDir, cacheKey+".txt")

			// Jika force, hapus cache meta agar trigger re-download
			if forceFlag {
				os.Remove(cachePath + ".meta")
			}

			fmt.Printf("[*] Source: %s\n", url)
			path, err := downloadToFile(url, cachePath, *connCount, client)
			if err != nil {
				fmt.Printf("[!] GAGAL: %v\n", err)
				fmt.Printf("[!] Source ini DILEWATI. File output tidak diubah oleh source ini.\n")
				skippedSources++
				continue
			}

			sourcePaths = append(sourcePaths, path)
			successSources++
		}
	}

	// 2b. File lokal (bisa lebih dari satu argumen)
	for _, inputFile := range flag.Args() {
		if _, err := os.Stat(inputFile); err != nil {
			fmt.Printf("[!] File '%s' tidak ditemukan. Dilewati.\n", inputFile)
			skippedSources++
			continue
		}
		fmt.Printf("[*] File lokal: %s\n", inputFile)
		sourcePaths = append(sourcePaths, inputFile)
		successSources++
	}

	// 2c. STDIN jika tidak ada argumen lain
	if *urlsFlag == "" && flag.NArg() == 0 {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			fmt.Println("[!] Tidak ada input yang diberikan.")
			showHelp()
		}
		// Untuk STDIN, simpan dulu ke temp file agar bisa di-streaming
		stdinCache := filepath.Join(cacheDir, "stdin.tmp")
		f, _ := os.Create(stdinCache)
		io.Copy(f, os.Stdin)
		f.Close()
		sourcePaths = append(sourcePaths, stdinCache)
		successSources++
	}

	// KEAMANAN: Batalkan build jika tidak ada satu pun source yang berhasil
	if len(sourcePaths) == 0 {
		fmt.Printf("[!] BATAL: Semua %d source gagal. File output '%s' TIDAK diubah.\n", skippedSources, *outputFile)
		os.Exit(1)
	}
	if skippedSources > 0 {
		fmt.Printf("[!] Peringatan: %d source dilewati. Build dilanjutkan dengan %d source.\n", skippedSources, successSources)
	}

	// --- 3. Build CDB (Streaming dari disk, memori minimal) ---
	tempFile := *outputFile + ".tmp"
	os.Remove(tempFile)

	writer, err := cdb.Create(tempFile)
	if err != nil {
		log.Fatalf("[!] Gagal membuat file DB sementara: %v", err)
	}

	countDomain := 0
	countIP := 0
	countWhitelisted := 0
	emptyValue := []byte{}
	wireKeyBuffer := make([]byte, 256)

	fmt.Printf("[*] Membangun CDB dari %d source (streaming)...\n", len(sourcePaths))

	// Iterasi setiap file source satu per satu (streaming, RAM rendah)
	for _, srcPath := range sourcePaths {
		f, err := os.Open(srcPath)
		if err != nil {
			fmt.Printf("[!] Gagal membuka '%s': %v. Dilewati.\n", srcPath, err)
			continue
		}

		// Buffer 1MB untuk performa baca line-by-line
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)

		for sc.Scan() {
			line := strings.TrimSpace(strings.ToLower(sc.Text()))
			
			// Bersihkan komentar inline
			if idx := strings.Index(line, "#"); idx != -1 {
				line = strings.TrimSpace(line[:idx])
			}
			if idx := strings.Index(line, "!"); idx != -1 { // Format komentar AdGuard
				line = strings.TrimSpace(line[:idx])
			}

			if line == "" || strings.HasPrefix(line, ";") {
				continue
			}

			// --- PARSER ADGUARD / HOSTS / DNSMASQ ---
			
			// Format Hosts: 0.0.0.0 domain.com atau 127.0.0.1 domain.com
			if strings.HasPrefix(line, "0.0.0.0 ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "0.0.0.0 "))
			} else if strings.HasPrefix(line, "127.0.0.1 ") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "127.0.0.1 "))
			}

			// Format AdGuard: ||domain.com^
			if strings.HasPrefix(line, "@@||") {
				continue // Skip whitelist rules
			}
			if strings.HasPrefix(line, "||") {
				line = strings.TrimPrefix(line, "||")
			}
			line = strings.TrimSuffix(line, "^")

			// Format Dnsmasq: address=/domain.com/0.0.0.0
			if strings.HasPrefix(line, "address=/") {
				parts := strings.Split(line, "/")
				if len(parts) >= 3 {
					line = parts[1]
				}
			}

			// Abaikan URL / Path filtering (Adblock Plus format non-DNS)
			if strings.Contains(line, "/") || strings.Contains(line, "*") {
				continue
			}

			if line == "" {
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

			if err := writer.Put(wireKey, emptyValue); err != nil {
				writer.Close()
				os.Remove(tempFile)
				f.Close()
				log.Fatalf("[!] Gagal menulis ke DB (disk penuh?): %v", err)
			}
		}

		f.Close()

		if err := sc.Err(); err != nil {
			fmt.Printf("[!] Peringatan: error membaca '%s': %v\n", srcPath, err)
		}
	}

	if err := writer.Close(); err != nil {
		os.Remove(tempFile)
		log.Fatalf("[!] Gagal menyimpan DB sementara: %v", err)
	}

	// ATOMIC REPLACE: Hanya ganti jika seluruh build sukses
	if err := os.Rename(tempFile, *outputFile); err != nil {
		os.Remove(tempFile)
		log.Fatalf("[!] Gagal mengganti file output: %v", err)
	}

	duration := time.Since(start)
	total := countDomain + countIP
	fmt.Printf("[+] SELESAI! %d entri (%d Domain, %d IP, %d whitelisted) -> '%s' dalam %v\n",
		total, countDomain, countIP, countWhitelisted, filepath.Base(*outputFile), duration)
}
