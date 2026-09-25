package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

const version = "1.0.0"

type options struct {
	configPath  string
	action      string
	zone        string
	dnsListen   string
	httpListen  string
	rawInput    string
	showVersion bool
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	fs := flag.NewFlagSet("rpz-master", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&opts.configPath, "c", "", "Path ke file konfigurasi JSON")
	fs.StringVar(&opts.action, "action", "serve", "Aksi yang dijalankan: serve | bootstrap | sync | build-cdb")
	fs.StringVar(&opts.zone, "zone", "", "Nama zone RPZ (override config)")
	fs.StringVar(&opts.dnsListen, "dns-listen", "", "Listen address DNS server (override config)")
	fs.StringVar(&opts.httpListen, "http-listen", "", "Listen address HTTP server (override config)")
	fs.StringVar(&opts.rawInput, "input", "", "File domain input lokal untuk build-cdb / bootstrap")
	fs.BoolVar(&opts.showVersion, "v", false, "Tampilkan versi")
	fs.BoolVar(&opts.showVersion, "version", false, "Tampilkan versi")
	return opts, fs.Parse(args)
}

type App struct {
	cfg           Config
	state         *State
	stateMu       sync.RWMutex
	updateMu      sync.Mutex
	publicationMu sync.RWMutex
}

func NewApp(cfg Config, state *State) *App { return &App{cfg: cfg, state: state} }

func (a *App) withUpdate(fn func() error) error {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return fn()
}

func (a *App) BuildCDB() error {
	return a.withUpdate(func() error { return a.buildCDB() })
}

func (a *App) buildCDB() error {
	res, err := CompileDomainsToCDB(a.cfg.RawDomainFile, a.cfg.CDBPath, true)
	if err != nil {
		return fmt.Errorf("compile CDB: %w", err)
	}
	a.stateMu.Lock()
	next := *a.state
	next.Deltas = append([]Delta(nil), a.state.Deltas...)
	next.TotalDomains = res.TotalEntries
	next.CDBSHA256 = res.SHA256
	next.LastUpdate = time.Now()
	if next.Serial == 0 && a.cfg.SourceMode != "rpz-slave" {
		now := time.Now()
		next.Serial = uint32(now.Year()*1000000 + int(now.Month())*10000 + now.Day()*100 + 1)
	}
	if err := next.Save(a.cfg.StatePath); err != nil {
		a.stateMu.Unlock()
		return fmt.Errorf("save state: %w", err)
	}
	*a.state = next
	a.stateMu.Unlock()
	return nil
}

func (a *App) Bootstrap() error {
	return a.withUpdate(func() error {
		if _, err := os.Stat(a.cfg.RawDomainFile); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat raw domain file: %w", err)
			}
			if a.cfg.SourceDomainURL == "" {
				return errors.New("raw domain file missing and source_domain_url is empty")
			}
			if err := DownloadDomainsToFile(a.cfg.SourceDomainURL, a.cfg.RawDomainFile); err != nil {
				return fmt.Errorf("download bootstrap: %w", err)
			}
		}
		res, err := CompileDomainsToCDB(a.cfg.RawDomainFile, a.cfg.CDBPath, true)
		if err != nil {
			return fmt.Errorf("compile bootstrap CDB: %w", err)
		}
		a.stateMu.Lock()
		next := *a.state
		next.TotalDomains = res.TotalEntries
		next.CDBSHA256 = res.SHA256
		next.LastUpdate = time.Now()
		if next.Serial == 0 && a.cfg.SourceMode != "rpz-slave" {
			next.Serial = uint32(time.Now().Year()*1000000 + int(time.Now().Month())*10000 + time.Now().Day()*100 + 1)
		}
		if err := next.Save(a.cfg.StatePath); err != nil {
			a.stateMu.Unlock()
			return fmt.Errorf("save bootstrap state: %w", err)
		}
		*a.state = next
		a.stateMu.Unlock()
		return nil
	})
}

func (a *App) Sync() error {
	return a.withUpdate(func() error {
		if a.cfg.UpstreamMaster == "" {
			return errors.New("upstream_master is not configured")
		}
		a.stateMu.RLock()
		snapshot := *a.state
		a.stateMu.RUnlock()
		if _, err := os.Stat(a.cfg.RawDomainFile); os.IsNotExist(err) {
			snapshot.Serial = 0
		} else if err != nil {
			return fmt.Errorf("stat raw domain file: %w", err)
		}
		slave, err := NewSlaveClient(a.cfg, &snapshot)
		if err != nil {
			return err
		}
		upstreamSerial, err := slave.QueryUpstreamSOA()
		if err != nil {
			return fmt.Errorf("query upstream SOA: %w", err)
		}
		if !serialGreater(upstreamSerial, snapshot.Serial) {
			return nil
		}
		delta, err := slave.SyncIXFR(upstreamSerial)
		if err != nil {
			return fmt.Errorf("sync IXFR: %w", err)
		}
		if delta == nil {
			return nil
		}
		a.publicationMu.Lock()
		defer a.publicationMu.Unlock()
		res, err := PublishDelta(a.cfg, delta)
		if err != nil {
			return fmt.Errorf("publish synced CDB: %w", err)
		}
		a.stateMu.Lock()
		next := *a.state
		next.Deltas = append(append([]Delta(nil), a.state.Deltas...), *delta)
		if len(next.Deltas) > 20 {
			next.Deltas = next.Deltas[len(next.Deltas)-20:]
		}
		next.Serial = upstreamSerial
		next.TotalDomains = res.TotalEntries
		next.CDBSHA256 = res.SHA256
		next.LastUpdate = time.Now()
		if err := next.Save(a.cfg.StatePath); err != nil {
			a.stateMu.Unlock()
			return fmt.Errorf("save synced state: %w", err)
		}
		*a.state = next
		a.stateMu.Unlock()
		return nil
	})
}

func (a *App) Serve(ctx context.Context) error {
	if _, err := os.Stat(a.cfg.RawDomainFile); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("stat raw domain file: %w", err)
		}
		if err := a.Sync(); err != nil {
			if a.cfg.SourceDomainURL == "" {
				return fmt.Errorf("initial RPZ transfer: %w", err)
			}
			log.Printf("[sync] initial RPZ transfer failed, trying bootstrap URL: %v", err)
			if err := a.Bootstrap(); err != nil {
				return fmt.Errorf("initial transfer and bootstrap failed: %w", err)
			}
		}
	} else if _, err := os.Stat(a.cfg.CDBPath); os.IsNotExist(err) {
		if err := a.BuildCDB(); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat CDB: %w", err)
	}

	rpz := NewRPZServerWithMutex(a.cfg, a.state, &a.stateMu, &a.publicationMu)
	tcpListener, err := net.Listen("tcp", a.cfg.ListenDNS)
	if err != nil {
		return fmt.Errorf("listen DNS TCP: %w", err)
	}
	defer tcpListener.Close()
	udpConn, err := net.ListenPacket("udp", a.cfg.ListenDNS)
	if err != nil {
		return fmt.Errorf("listen DNS UDP: %w", err)
	}
	defer udpConn.Close()
	tcpServer := &dns.Server{Listener: tcpListener, Net: "tcp", Handler: rpz}
	udpServer := &dns.Server{PacketConn: udpConn, Net: "udp", Handler: rpz}
	if a.cfg.TSIGKey != "" {
		secret, err := a.cfg.ReadTSIGSecret()
		if err != nil {
			return err
		}
		tsig := map[string]string{dns.Fqdn(a.cfg.TSIGKey): secret}
		tcpServer.TsigSecret, udpServer.TsigSecret = tsig, tsig
	}

	var httpSrv *HTTPServer
	var httpListener net.Listener
	if a.cfg.ListenHTTP != "" {
		httpListener, err = net.Listen("tcp", a.cfg.ListenHTTP)
		if err != nil {
			return fmt.Errorf("listen HTTP: %w", err)
		}
		httpSrv = NewHTTPServer(a.cfg, a.state, &a.stateMu)
	}
	errCh := make(chan error, 3)
	go func() { errCh <- tcpServer.ActivateAndServe() }()
	go func() { errCh <- udpServer.ActivateAndServe() }()
	if httpSrv != nil {
		go func() { errCh <- httpSrv.Serve(httpListener) }()
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if a.cfg.UpstreamMaster != "" {
		interval, _ := time.ParseDuration(a.cfg.CheckInterval)
		go a.periodicSync(workerCtx, interval)
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
		if serveErr != nil {
			serveErr = fmt.Errorf("server stopped: %w", serveErr)
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	var shutdownErrs []error
	if httpSrv != nil {
		shutdownErrs = append(shutdownErrs, httpSrv.Shutdown(shutdownCtx))
	}
	for _, server := range []*dns.Server{tcpServer, udpServer} {
		if err := server.ShutdownContext(shutdownCtx); err != nil && err.Error() != "dns: server not started" {
			shutdownErrs = append(shutdownErrs, err)
		}
	}
	shutdownErrs = append(shutdownErrs, serveErr)
	return errors.Join(shutdownErrs...)
}

func (a *App) periodicSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.Sync(); err != nil {
				log.Printf("[sync] periodic sync failed: %v", err)
			}
		}
	}
}

func run(args []string, output io.Writer) error {
	opts, err := parseOptions(args, output)
	if err != nil {
		return err
	}
	if opts.showVersion {
		_, err := fmt.Fprintf(output, "rpz-master v%s - High-Performance Streaming RPZ Master & CDB Distributor\n", version)
		return err
	}
	cfg, err := LoadConfig(opts.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if opts.zone != "" {
		cfg.Zone = opts.zone
	}
	if opts.dnsListen != "" {
		cfg.ListenDNS = opts.dnsListen
	}
	if opts.httpListen != "" {
		cfg.ListenHTTP = opts.httpListen
	}
	if opts.rawInput != "" {
		cfg.RawDomainFile = opts.rawInput
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if opts.action == "serve" || opts.action == "sync" {
		if err := cfg.ValidateDaemon(); err != nil {
			return err
		}
	}
	state, err := LoadState(cfg.StatePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	app := NewApp(cfg, state)
	switch opts.action {
	case "bootstrap":
		return app.Bootstrap()
	case "build-cdb":
		return app.BuildCDB()
	case "sync":
		return app.Sync()
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return app.Serve(ctx)
	default:
		return fmt.Errorf("unknown action %q", opts.action)
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		log.Printf("[!] %v", err)
		os.Exit(1)
	}
}
