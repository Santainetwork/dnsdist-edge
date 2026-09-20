//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

const (
	ipTransparent = 19
	ipOrigDstAddr = 20
	maxDNSPacket  = 65535
)

var proxySignature = []byte("\r\n\r\n\x00\r\nQUIT\n")

type config struct {
	listen, backend, source string
	timeout                 time.Duration
	maxInflight             int
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listen, "listen", "0.0.0.0:5300", "TPROXY listen address")
	flag.StringVar(&cfg.backend, "backend", "127.0.0.1:5353", "dnsdist PROXYv2 listener")
	flag.StringVar(&cfg.source, "source", "127.0.0.2", "trusted loopback source sent to dnsdist")
	flag.DurationVar(&cfg.timeout, "timeout", 30*time.Second, "UDP query and TCP session timeout")
	flag.IntVar(&cfg.maxInflight, "max-inflight", 4096, "maximum concurrent UDP queries and TCP sessions")
	flag.Parse()
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	if cfg.maxInflight < 1 || cfg.timeout <= 0 {
		return errors.New("max-inflight and timeout must be positive")
	}
	listen, err := net.ResolveTCPAddr("tcp4", cfg.listen)
	if err != nil {
		return err
	}
	backendTCP, err := net.ResolveTCPAddr("tcp4", cfg.backend)
	if err != nil {
		return err
	}
	backendUDP, err := net.ResolveUDPAddr("udp4", cfg.backend)
	if err != nil {
		return err
	}
	source := net.ParseIP(cfg.source).To4()
	if source == nil || !source.IsLoopback() {
		return errors.New("source must be an IPv4 loopback address")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	lc := transparentListenConfig(true)
	packet, err := lc.ListenPacket(ctx, "udp4", cfg.listen)
	if err != nil {
		return fmt.Errorf("UDP listen: %w", err)
	}
	defer packet.Close()
	tcpListener, err := lc.Listen(ctx, "tcp4", cfg.listen)
	if err != nil {
		return fmt.Errorf("TCP listen: %w", err)
	}
	defer tcpListener.Close()
	sem := make(chan struct{}, cfg.maxInflight)
	errch := make(chan error, 2)
	go serveUDP(ctx, packet.(*net.UDPConn), backendUDP, source, cfg.timeout, sem, errch)
	go serveTCP(ctx, tcpListener, backendTCP, source, cfg.timeout, sem, errch)
	log.Printf("TPROXY listening on %s, dnsdist backend %s", listen, cfg.backend)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errch:
		return err
	}
}

func transparentListenConfig(originalDestination bool) net.ListenConfig {
	return net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var sockErr error
		err := raw.Control(func(fd uintptr) {
			sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_IP, ipTransparent, 1)
			if sockErr == nil {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			}
			if sockErr == nil && originalDestination {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_IP, ipOrigDstAddr, 1)
			}
		})
		if err != nil {
			return err
		}
		return sockErr
	}}
}

func serveUDP(ctx context.Context, listener *net.UDPConn, backend *net.UDPAddr, source net.IP, timeout time.Duration, sem chan struct{}, errch chan<- error) {
	for {
		buf, oob := make([]byte, maxDNSPacket), make([]byte, 256)
		n, oobn, _, client, err := listener.ReadMsgUDP(buf, oob)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errch <- err
			return
		}
		if n < 12 {
			continue
		}
		destination, err := originalDestination(oob[:oobn])
		if err != nil {
			log.Printf("drop UDP query from %s: %v", client, err)
			continue
		}
		select {
		case sem <- struct{}{}:
			query := append([]byte(nil), buf[:n]...)
			go func() {
				defer func() { <-sem }()
				if err := proxyUDP(listener, query, client, destination, backend, source, timeout); err != nil {
					log.Printf("UDP %s: %v", client, err)
				}
			}()
		default:
			log.Printf("drop UDP query from %s: max inflight reached", client)
		}
	}
}

func proxyUDP(listener *net.UDPConn, query []byte, client, destination, backend *net.UDPAddr, source net.IP, timeout time.Duration) error {
	conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: source}, backend)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	header, err := proxyV2(client, destination, false)
	if err != nil {
		return err
	}
	if _, err = conn.Write(append(header, query...)); err != nil {
		return err
	}
	response := make([]byte, maxDNSPacket)
	n, err := conn.Read(response)
	if err != nil {
		return err
	}
	if n < 12 {
		return errors.New("short DNS response")
	}
	_, _, err = listener.WriteMsgUDP(response[:n], sourceControl(destination.IP), client)
	return err
}

func sourceControl(ip net.IP) []byte {
	data := make([]byte, 12)
	copy(data[4:8], ip.To4())
	oob := make([]byte, syscall.CmsgSpace(len(data)))
	putCmsg(oob, syscall.IP_PKTINFO, data)
	return oob
}

func serveTCP(ctx context.Context, listener net.Listener, backend *net.TCPAddr, source net.IP, timeout time.Duration, sem chan struct{}, errch chan<- error) {
	for {
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errch <- err
			return
		}
		select {
		case sem <- struct{}{}:
			go func() { defer func() { <-sem }(); proxyTCP(client, backend, source, timeout) }()
		default:
			_ = client.Close()
		}
	}
}

func proxyTCP(client net.Conn, backend *net.TCPAddr, source net.IP, timeout time.Duration) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(timeout))
	destination, ok := client.LocalAddr().(*net.TCPAddr)
	if !ok {
		return
	}
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: source}, Timeout: timeout}
	upstream, err := dialer.Dial("tcp4", backend.String())
	if err != nil {
		log.Printf("TCP %s: %v", client.RemoteAddr(), err)
		return
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(time.Now().Add(timeout))
	header, err := proxyV2(client.RemoteAddr(), destination, true)
	if err != nil {
		return
	}
	_ = upstream.SetWriteDeadline(time.Now().Add(timeout))
	if _, err = upstream.Write(header); err != nil {
		return
	}
	_ = upstream.SetWriteDeadline(time.Time{})
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, client)
		if c, ok := upstream.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, upstream)
	<-done
}

func proxyV2(src, dst net.Addr, tcp bool) ([]byte, error) {
	srcIP, srcPort, err := address(src)
	if err != nil {
		return nil, err
	}
	dstIP, dstPort, err := address(dst)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 28)
	copy(out, proxySignature)
	out[12] = 0x21
	if tcp {
		out[13] = 0x11
	} else {
		out[13] = 0x12
	}
	binary.BigEndian.PutUint16(out[14:16], 12)
	copy(out[16:20], srcIP)
	copy(out[20:24], dstIP)
	binary.BigEndian.PutUint16(out[24:26], uint16(srcPort))
	binary.BigEndian.PutUint16(out[26:28], uint16(dstPort))
	return out, nil
}

func address(addr net.Addr) (net.IP, int, error) {
	var ip net.IP
	var port int
	switch value := addr.(type) {
	case *net.UDPAddr:
		ip, port = value.IP, value.Port
	case *net.TCPAddr:
		ip, port = value.IP, value.Port
	default:
		return nil, 0, fmt.Errorf("unsupported address %T", addr)
	}
	ip = ip.To4()
	if ip == nil || port < 0 || port > 65535 {
		return nil, 0, errors.New("IPv4 address and valid port required")
	}
	return ip, port, nil
}

func originalDestination(oob []byte) (*net.UDPAddr, error) {
	messages, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.Header.Level == syscall.IPPROTO_IP && message.Header.Type == ipOrigDstAddr && len(message.Data) >= 8 {
			if binary.NativeEndian.Uint16(message.Data[:2]) != syscall.AF_INET {
				continue
			}
			return &net.UDPAddr{IP: net.IPv4(message.Data[4], message.Data[5], message.Data[6], message.Data[7]), Port: int(binary.BigEndian.Uint16(message.Data[2:4]))}, nil
		}
	}
	return nil, errors.New("original destination missing")
}

func cmsgSpace(length int) int { return syscall.CmsgSpace(length) }
func putCmsg(buffer []byte, typ int32, data []byte) {
	header := (*syscall.Cmsghdr)(unsafe.Pointer(&buffer[0]))
	header.Level = syscall.IPPROTO_IP
	header.Type = typ
	header.SetLen(syscall.CmsgLen(len(data)))
	copy(buffer[syscall.CmsgLen(0):], data)
}
