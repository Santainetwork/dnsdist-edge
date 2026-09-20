//go:build linux

package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"testing"
)

func TestProxyV2IPv4(t *testing.T) {
	src := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 12345}
	dst := &net.UDPAddr{IP: net.ParseIP("8.8.8.8"), Port: 53}
	got, err := proxyV2(src, dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 28 {
		t.Fatalf("length=%d", len(got))
	}
	if got[12] != 0x21 || got[13] != 0x12 {
		t.Fatalf("command/family=%x %x", got[12], got[13])
	}
	if binary.BigEndian.Uint16(got[14:16]) != 12 {
		t.Fatal("bad address length")
	}
	if !net.IP(got[16:20]).Equal(src.IP) || !net.IP(got[20:24]).Equal(dst.IP) {
		t.Fatal("bad addresses")
	}
	if binary.BigEndian.Uint16(got[24:26]) != 12345 || binary.BigEndian.Uint16(got[26:28]) != 53 {
		t.Fatal("bad ports")
	}
}

func TestProxyV2RejectsIPv6(t *testing.T) {
	_, err := proxyV2(&net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 1}, &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 53}, false)
	if err == nil {
		t.Fatal("IPv6 must be rejected in v1")
	}
}

func TestParseOriginalDestination(t *testing.T) {
	data := make([]byte, 16)
	binary.NativeEndian.PutUint16(data[0:2], 2)
	binary.BigEndian.PutUint16(data[2:4], 53)
	copy(data[4:8], net.ParseIP("8.8.4.4").To4())
	oob := make([]byte, cmsgSpace(len(data)))
	putCmsg(oob, ipOrigDstAddr, data)
	got, err := originalDestination(oob)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "8.8.4.4:53" {
		t.Fatalf("got %s", got)
	}
}

func TestSourceControl(t *testing.T) {
	messages, err := syscall.ParseSocketControlMessage(sourceControl(net.ParseIP("8.8.8.8")))
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
	message := messages[0]
	if message.Header.Type != syscall.IP_PKTINFO || !net.IP(message.Data[4:8]).Equal(net.ParseIP("8.8.8.8")) {
		t.Fatalf("invalid IP_PKTINFO: type=%d data=%x", message.Header.Type, message.Data)
	}
}
