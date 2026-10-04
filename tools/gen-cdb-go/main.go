// Command gen-cdb-go is a fast, drop-in replacement for tools/gen-cdb.py.
//
// It builds a CDB (Constant Database) blacklist for DNSDist. By default keys
// are written in DNS WIRE format (RFC 1035 length-prefixed labels + trailing
// zero byte), which is the format DNSDist looks up with
// KeyValueLookupKeyQName(true) and the format trust-builder produces with
// dns.PackDomainName. The legacy Python script wrote plain-text keys
// ("pornhub.com."), which never matched DNSDist's wire lookup; that bug is
// fixed here.
//
// Usage:
//
//	gen-cdb-go OUTPUT.db domain1.com domain2.com...
//	cat domains.txt | gen-cdb-go OUTPUT.db
//	gen-cdb-go OUTPUT.db --value 1 --no-wire <<< "evil.com"
//
// Flags:
//
//	--value STR  value byte(s) for every key (default "x")
//	--no-wire    write plain-text keys instead of DNS wire format
//	-h, --help   show help
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/colinmarc/cdb"
	"github.com/miekg/dns"
)

const usage = `gen-cdb-go — Generator blacklist CDB untuk DNSDist (format wire).

Membuat file CDB (Constant Database) berisi pasangan key-value:
  key   = nama domain dalam DNS wire-format (RFC 1035, default)
  value = byte bebas (default: 'x', bisa diubah via --value)

Format wire wajib agar cocok dengan DNSDist:
  kvs     = newCDBKVStore('/var/lib/dnsdist/blacklist.db', 5)
  kvsRule = KeyValueStoreLookupRule(kvs, KeyValueLookupKeyQName(true))

Penggunaan:
  gen-cdb-go OUTPUT.db domain1.com domain2.com...
  cat domains.txt | gen-cdb-go OUTPUT.db
  gen-cdb-go OUTPUT.db --value '1' --no-wire <<< "evil.com"

Opsi:
  --value STR    value byte (string) untuk setiap key (default: 'x')
  --no-wire      pakai key plain-text (tanpa format wire)
  -h, --help     tampilkan bantuan ini

Domain dinormalisasi (huruf kecil, titik akhir dihapus) dan divalidasi:
label <= 63 byte, total nama <= 253 karakter. Entri tidak valid dilewati
dengan peringatan ke stderr.
`

// maxNameLen is the maximum length of a domain in presentation form
// ("a.b.c"). A 253-character name expands to at most 255 wire octets.
const maxNameLen = 253

// maxLabelLen is the maximum length of a single DNS label.
const maxLabelLen = 63

// normalize lowercases a domain and strips any trailing dots.
func normalize(domain string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(domain)), ".")
}

// wireName converts a presentation-form domain into DNS wire format
// (length-prefixed labels + trailing zero byte), e.g.
//
//	"pornhub.com" -> "\x07pornhub\x03com\x00"
//
// It returns an error if the domain is empty or exceeds DNS limits.
func wireName(domain string) ([]byte, error) {
	d := normalize(domain)
	if d == "" {
		return nil, fmt.Errorf("domain kosong: %q", domain)
	}
	if len(d) > maxNameLen {
		return nil, fmt.Errorf("nama %d > %d karakter: %q", len(d), maxNameLen, domain)
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" {
			return nil, fmt.Errorf("label kosong: %q", domain)
		}
		if len(label) > maxLabelLen {
			return nil, fmt.Errorf("label %d > %d byte: %q", len(label), maxLabelLen, domain)
		}
	}

	buf := make([]byte, 256)
	off, err := dns.PackDomainName(d+".", buf, 0, nil, false)
	if err != nil {
		return nil, fmt.Errorf("nama tidak valid %q: %w", domain, err)
	}
	return buf[:off], nil
}

// keyFor returns the CDB key for a domain, either wire format (default) or
// plain text, plus an error describing why the domain was rejected.
func keyFor(domain string, wire bool) ([]byte, error) {
	if wire {
		return wireName(domain)
	}

	d := normalize(domain)
	if d == "" {
		return nil, fmt.Errorf("domain kosong: %q", domain)
	}
	if len(d) > maxNameLen {
		return nil, fmt.Errorf("nama %d > %d karakter: %q", len(d), maxNameLen, domain)
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" {
			return nil, fmt.Errorf("label kosong: %q", domain)
		}
		if len(label) > maxLabelLen {
			return nil, fmt.Errorf("label %d > %d byte: %q", len(label), maxLabelLen, domain)
		}
	}
	return []byte(d), nil
}

// run executes the CLI and returns a process exit code. It is separated from
// main so tests can drive it with in-memory streams.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 2
	}

	value := "x"
	wire := true
	out := ""
	var domains []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(stdout, usage)
			return 0
		case a == "--value":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "[!] --value membutuhkan argumen")
				return 2
			}
			i++
			value = args[i]
		case strings.HasPrefix(a, "--value="):
			value = strings.TrimPrefix(a, "--value=")
		case a == "--no-wire":
			wire = false
		case out == "":
			out = a
		default:
			domains = append(domains, a)
		}
	}

	if out == "" {
		fmt.Fprintln(stderr, "[!] Tentukan file output: gen-cdb-go OUTPUT.db domain...")
		return 2
	}

	// Read domains from stdin when none were given on the command line.
	if len(domains) == 0 {
		sc := bufio.NewScanner(stdin)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				domains = append(domains, line)
			}
		}
		if err := sc.Err(); err != nil {
			fmt.Fprintf(stderr, "[!] Gagal membaca stdin: %v\n", err)
			return 2
		}
	}

	valueBytes := []byte(value)
	writer, err := cdb.Create(out)
	if err != nil {
		fmt.Fprintf(stderr, "[!] Gagal membuat file DB: %v\n", err)
		return 1
	}

	written := 0
	for _, d := range domains {
		key, err := keyFor(d, wire)
		if err != nil {
			fmt.Fprintf(stderr, "[!] Lewati: %v\n", err)
			continue
		}
		if err := writer.Put(key, valueBytes); err != nil {
			writer.Close()
			fmt.Fprintf(stderr, "[!] Gagal menulis ke DB (disk penuh?): %v\n", err)
			return 1
		}
		written++
	}

	if written == 0 {
		writer.Close()
		fmt.Fprintln(stderr, "[!] Tidak ada domain valid untuk ditulis.")
		return 2
	}

	if err := writer.Close(); err != nil {
		fmt.Fprintf(stderr, "[!] Gagal menyimpan DB: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "[✓] OK: %d entri ditulis\n", written)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
