package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestScanBounds(t *testing.T) {
	adapter := Adapter{Address: "10.0.0.10", CIDR: "10.0.0.10/16"}
	cidr, hosts, err := scanRange(adapter, "")
	if err != nil || cidr != "10.0.0.0/24" || len(hosts) != 253 {
		t.Fatal(cidr, len(hosts), err)
	}
	for _, host := range hosts {
		if host == "10.0.0.10" || host == "10.0.0.0" || host == "10.0.0.255" {
			t.Fatal(host)
		}
	}
	for _, invalid := range []string{"10.1.0.0/24", "10.0.0.0/21", "8.8.8.8/32", "10.0.0.1/33", "bad"} {
		if _, _, err := scanRange(adapter, invalid); err == nil {
			t.Fatal("accepted scan", invalid)
		}
	}
	_, hosts, err = scanRange(Adapter{Address: "10.0.0.10", CIDR: "10.0.0.10/32"}, "")
	if err != nil || len(hosts) != 0 {
		t.Fatal(hosts, err)
	}
}
func TestMDNSRecordsResolveSplitPacketsAndRejectForeignNetworks(t *testing.T) {
	cache := newMDNSCache("10.0.0.10/24")
	source := net.ParseIP("10.0.0.223")
	ingest := func(ip net.IP, records ...dns.RR) {
		m := dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: records}
		raw, err := m.Pack()
		if err != nil {
			t.Fatal(err)
		}
		cache.ingest(raw, ip)
	}
	ptr := &dns.PTR{Hdr: dns.RR_Header{Name: "_http._tcp.local.", Rrtype: dns.TypePTR, Class: 1, Ttl: 120}, Ptr: "TC1._http._tcp.local."}
	srv := &dns.SRV{Hdr: dns.RR_Header{Name: ptr.Ptr, Rrtype: dns.TypeSRV, Class: 1, Ttl: 120}, Target: "tc1.local.", Port: 8088}
	address := &dns.A{Hdr: dns.RR_Header{Name: "tc1.local.", Rrtype: dns.TypeA, Class: 1, Ttl: 120}, A: source}
	ingest(source, ptr)
	ingest(source, srv)
	if len(cache.addresses()) != 0 {
		t.Fatal("unresolved address accepted")
	}
	ingest(net.ParseIP("10.1.0.1"), address)
	if len(cache.addresses()) != 0 {
		t.Fatal("off-network sender accepted")
	}
	ingest(source, address)
	if fmt.Sprint(cache.addresses()) != "[http://10.0.0.223:8088]" {
		t.Fatal(cache.addresses())
	}
	srv.Hdr.Name = "TC1._easylink._tcp.local."
	srv.Port = 8000
	ingest(source, srv)
	if len(cache.addresses()) != 2 {
		t.Fatal("EasyLink missing", cache.addresses())
	}
	address.Hdr.Name = "bad.local."
	address.A = net.ParseIP("8.8.8.8")
	ingest(source, address)
	if len(cache.hosts["bad.local."]) != 0 {
		t.Fatal("public candidate accepted")
	}
}
func TestDiscoveryWithoutMDNSAndCancellation(t *testing.T) {
	m, a, _ := testManager(t)
	f := firmwareFixture(t, "TC1", "aabbcc000091")
	f.legacy = true
	delete(f.state, "model")
	u, _ := url.Parse(f.server.URL)
	port := freePort(t)
	fmt.Sscanf(u.Port(), "%d", &port)
	d := a.discovery
	d.interfaces = func() []Adapter {
		return []Adapter{{ID: "test", Label: "Test", Address: "127.0.0.2", CIDR: "127.0.0.2/30"}}
	}
	d.port = port
	d.browse = func(context.Context, Adapter) ([]string, string) { return []string{}, "complete" }
	if _, err := d.start(Obj{"mode": "mdns"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("empty mDNS triggered active scan")
	}
	if _, err := d.start(Obj{"mode": "lan"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	snapshot := d.snapshot()
	if snapshot["checked"] != 1 || len(snapshot["devices"].([]Obj)) != 1 || len(m.list()) != 0 {
		t.Fatal(snapshot)
	}
	device := snapshot["devices"].([]Obj)[0]
	if device["id"] != "tc1-aabbcc000091" || device["source"] != "lan" {
		t.Fatal(device)
	}
	d.browse = func(context.Context, Adapter) ([]string, string) { return []string{f.server.URL}, "complete" }
	if _, err := d.start(Obj{"mode": "mdns"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	if len(d.snapshot()["devices"].([]Obj)) != 1 {
		t.Fatal("mDNS candidate verification failed")
	}
	// The default action combines both transports and deduplicates by identity.
	if _, err := d.start(Obj{}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	snapshot = d.snapshot()
	if snapshot["mode"] != "auto" || snapshot["checked"] != 1 || len(snapshot["devices"].([]Obj)) != 1 {
		t.Fatal("combined discovery did not merge results", snapshot)
	}
	d.browse = func(context.Context, Adapter) ([]string, string) { return nil, "unavailable" }
	if _, err := d.start(Obj{}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	if len(d.snapshot()["devices"].([]Obj)) != 1 {
		t.Fatal("unavailable mDNS prevented LAN discovery")
	}
	browseStarted, probeStarted := make(chan struct{}), make(chan struct{})
	d.browse = func(ctx context.Context, _ Adapter) ([]string, string) {
		close(browseStarted)
		<-ctx.Done()
		return nil, "unavailable"
	}
	d.probe = func(ctx context.Context, _, _ string, _ int) bool { close(probeStarted); <-ctx.Done(); return false }
	if _, err := d.start(Obj{}); err != nil {
		t.Fatal(err)
	}
	<-browseStarted
	<-probeStarted
	start := time.Now()
	d.cancel()
	d.wg.Wait()
	if time.Since(start) > time.Second || d.snapshot()["state"] != "cancelled" {
		t.Fatal("cancellation failed")
	}
}

func TestDiscoveryFiltersContainerInterfaces(t *testing.T) {
	for _, name := range []string{"br-ab5f9f2ed00f", "docker0", "veth123", "virbr0", "cni0", "podman0", "tailscale0", "wg0", "vEthernet (Default Switch)"} {
		if usableInterface(net.Interface{Name: name, Flags: net.FlagUp}) {
			t.Fatal("virtual interface exposed", name)
		}
	}
	for _, name := range []string{"enp4s0", "eth0", "wlan0", "bond0", "br0", "Ethernet"} {
		if !usableInterface(net.Interface{Name: name, Flags: net.FlagUp}) {
			t.Fatal("LAN interface excluded", name)
		}
	}
	if usableInterface(net.Interface{Name: "eth0"}) || usableInterface(net.Interface{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}) {
		t.Fatal("inactive or loopback interface accepted")
	}
}
