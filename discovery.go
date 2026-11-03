package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type Adapter struct {
	ID, Label, Address, CIDR string
	Index                    int
}

func usableInterface(iface net.Interface) bool {
	if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
		return false
	}
	name := strings.ToLower(iface.Name)
	for _, part := range []string{"docker", "veth", "tailscale", "zerotier", "loopback", "vethernet", "virtualbox", "vmware", "tun", "tap"} {
		if strings.Contains(name, part) {
			return false
		}
	}
	for _, prefix := range []string{"br-", "virbr", "cni", "flannel", "podman", "wg"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

func localInterfaces() []Adapter {
	out := []Adapter{}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if !usableInterface(iface) {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, network, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || !privateIP(ip) {
				continue
			}
			prefix, _ := network.Mask.Size()
			out = append(out, Adapter{ip.String(), iface.Name + " · " + ip.String(), ip.String(), fmt.Sprintf("%s/%d", ip.String(), prefix), iface.Index})
		}
	}
	if len(out) == 0 {
		out = fallbackInterfaces()
	}
	preferred := routeSourceIP(net.IPv4(192, 0, 2, 1)).String()
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Address == preferred) != (out[j].Address == preferred) {
			return out[i].Address == preferred
		}
		return out[i].Index < out[j].Index
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}
func ipNumber(ip net.IP) uint32 { return binary.BigEndian.Uint32(ip.To4()) }
func numberIP(value uint32) net.IP {
	out := make(net.IP, 4)
	binary.BigEndian.PutUint32(out, value)
	return out
}
func scanRange(adapter Adapter, requested string) (string, []string, error) {
	requested = strings.TrimSpace(requested)
	if ip := net.ParseIP(requested); ip != nil && ip.To4() != nil {
		requested = ip.String() + "/32"
	}
	own, network, err := net.ParseCIDR(adapter.CIDR)
	ip := net.ParseIP(adapter.Address)
	manual := adapter == (Adapter{}) && requested != ""
	if !manual && (err != nil || own.To4() == nil || ip.To4() == nil || !privateIP(ip) || !network.Contains(ip)) {
		return "", nil, failure("网络接口缺少有效网段，无法扫描", 422, "invalid_params")
	}
	if requested == "" {
		prefix, _ := network.Mask.Size()
		requested = network.String()
		if prefix < 24 {
			requested = adapter.Address + "/24"
		}
	}
	rangeIP, subnet, err := net.ParseCIDR(requested)
	if err != nil || rangeIP.To4() == nil {
		return "", nil, failure("请填写内网 IPv4 地址或网段，例如 192.168.1.20 或 192.168.1.0/24", 422, "invalid_params")
	}
	bits, width := subnet.Mask.Size()
	if width != 32 {
		return "", nil, failure("扫描仅支持 IPv4 地址或网段", 422, "invalid_params")
	}
	if bits < 22 {
		return "", nil, failure("每次最多扫描 1024 个地址（/22 至 /32）", 422, "invalid_params")
	}
	first := uint64(ipNumber(subnet.IP))
	last := first + (uint64(1) << uint(32-bits)) - 1
	if !privateIP(numberIP(uint32(first))) || !privateIP(numberIP(uint32(last))) {
		return "", nil, failure("仅支持扫描内网 IPv4 地址或网段", 422, "invalid_params")
	}
	if !manual && (!network.Contains(numberIP(uint32(first))) || !network.Contains(numberIP(uint32(last)))) {
		return "", nil, failure("扫描网段必须位于所选 NAS 网络接口内", 422, "invalid_params")
	}
	if bits < 31 {
		first++
		last--
	}
	hosts := []string{}
	for n := first; n <= last; n++ {
		host := numberIP(uint32(n)).String()
		if host != adapter.Address {
			hosts = append(hosts, host)
		}
	}
	return subnet.String(), hosts, nil
}
func probeHTTP(ctx context.Context, host, local string, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	dialer := net.Dialer{}
	if ip := net.ParseIP(local); ip != nil {
		dialer.LocalAddr = &net.TCPAddr{IP: ip}
	}
	conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

var mdnsServices = []string{"_easylink._tcp.local.", "_phicomm._tcp.local.", "_http._tcp.local.", "_openiot._udp.local."}

type mdnsInstance struct {
	service, target string
	port            uint16
}
type mdnsCache struct {
	network   *net.IPNet
	instances map[string]mdnsInstance
	hosts     map[string][]string
}

func newMDNSCache(cidr string) *mdnsCache {
	_, network, _ := net.ParseCIDR(cidr)
	return &mdnsCache{network: network, instances: map[string]mdnsInstance{}, hosts: map[string][]string{}}
}
func dnsName(s string) string { return strings.ToLower(dns.Fqdn(s)) }
func serviceOf(name string) string {
	for _, service := range mdnsServices {
		if strings.HasSuffix(name, "."+service) {
			return service
		}
	}
	return ""
}
func (c *mdnsCache) allowed(ip net.IP) bool {
	return ip != nil && ip.To4() != nil && privateIP(ip) && c.network != nil && c.network.Contains(ip)
}
func (c *mdnsCache) ingest(data []byte, source net.IP) {
	if !c.allowed(source) {
		return
	}
	var message dns.Msg
	if message.Unpack(data) != nil || !message.Response {
		return
	}
	records := append(message.Answer, message.Extra...)
	if len(records) > 256 {
		records = records[:256]
	}
	for _, record := range records {
		header := record.Header()
		if header.Ttl == 0 || len(header.Name) > 255 {
			continue
		}
		name := dnsName(header.Name)
		switch r := record.(type) {
		case *dns.PTR:
			if !contains(mdnsServices, name) {
				continue
			}
			instance := dnsName(r.Ptr)
			if !strings.HasSuffix(instance, "."+name) {
				continue
			}
			if _, ok := c.instances[instance]; ok || len(c.instances) < 128 {
				current := c.instances[instance]
				current.service = name
				c.instances[instance] = current
			}
		case *dns.SRV:
			service := serviceOf(name)
			target := dnsName(r.Target)
			if service == "" || !strings.HasSuffix(target, ".local.") || r.Port == 0 {
				continue
			}
			if _, ok := c.instances[name]; ok || len(c.instances) < 128 {
				c.instances[name] = mdnsInstance{service, target, r.Port}
			}
		case *dns.A:
			if !strings.HasSuffix(name, ".local.") || !c.allowed(r.A) {
				continue
			}
			if _, ok := c.hosts[name]; ok || len(c.hosts) < 128 {
				ip := r.A.String()
				if len(c.hosts[name]) < 4 && !contains(c.hosts[name], ip) {
					c.hosts[name] = append(c.hosts[name], ip)
				}
			}
		}
	}
}
func (c *mdnsCache) addresses() []string {
	found := map[string]bool{}
	out := []string{}
	for _, instance := range c.instances {
		port := instance.port
		if instance.service == mdnsServices[0] || instance.service == "_openiot._udp.local." {
			port = 80
		}
		if instance.target == "" || port == 0 {
			continue
		}
		for _, host := range c.hosts[instance.target] {
			address := "http://" + host
			if port != 80 {
				address += fmt.Sprintf(":%d", port)
			}
			if !found[address] {
				found[address] = true
				out = append(out, address)
				if len(out) >= 64 {
					return out
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
func browseMDNS(ctx context.Context, adapter Adapter) ([]string, string) {
	iface, err := net.InterfaceByIndex(adapter.Index)
	if err != nil {
		// A restricted NAS account may be unable to enumerate netlink interfaces.
		// Let the kernel choose its default multicast interface in that case.
		iface = nil
	}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	socket, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		return nil, "unavailable"
	}
	defer socket.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cache := newMDNSCache(adapter.CIDR)
	asked := map[string]bool{}
	query := func(name string, kind uint16) {
		if len(asked) >= 256 {
			return
		}
		asked[name] = true
		message := new(dns.Msg)
		message.SetQuestion(name, kind)
		message.Id = 0
		message.RecursionDesired = false
		raw, err := message.Pack()
		if err == nil {
			socket.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			socket.WriteToUDP(raw, group)
		}
	}
	askAll := func() {
		for _, name := range mdnsServices {
			query(name, dns.TypePTR)
		}
		for name, instance := range cache.instances {
			if instance.target == "" {
				query(name, dns.TypeSRV)
			} else if len(cache.hosts[instance.target]) == 0 {
				query(instance.target, dns.TypeA)
			}
		}
	}
	askAll()
	next := time.Now().Add(time.Second)
	buffer := make([]byte, 65536)
	for ctx.Err() == nil {
		socket.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, source, err := socket.ReadFromUDP(buffer)
		if err == nil {
			cache.ingest(buffer[:n], source.IP)
			for name, instance := range cache.instances {
				target, kind := name, uint16(dns.TypeSRV)
				if instance.target != "" {
					if len(cache.hosts[instance.target]) > 0 {
						continue
					}
					target = instance.target
					kind = dns.TypeA
				}
				if !asked[target] {
					query(target, kind)
				}
			}
		} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
			return nil, "unavailable"
		}
		if time.Now().After(next) {
			askAll()
			next = time.Now().Add(time.Second)
		}
	}
	return cache.addresses(), "complete"
}

type discoveryScan struct {
	state, phase, mdns, mode, adapter, rangeCIDR string
	checked, total                               int
	devices                                      map[string]Obj
	protected                                    []string
	ctx                                          context.Context
	cancel                                       context.CancelFunc
	background                                   bool
	viewers                                      map[string]time.Time
	recovery                                     map[string]lanRecoveryTarget
}

type discoveryTarget struct {
	adapter Adapter
	cidr    string
	hosts   []string
}

func discoveryTargets(interfaces []Adapter, selected, requested, mode string) ([]discoveryTarget, error) {
	requested = strings.TrimSpace(requested)
	adapters := interfaces
	if selected != "" {
		adapters = nil
		for _, a := range interfaces {
			if a.ID == selected {
				adapters = []Adapter{a}
				break
			}
		}
		if len(adapters) == 0 {
			return nil, failure("所选网络接口已不可用，请改为自动选择后重试", 422, "invalid_params")
		}
	} else if requested != "" && mode != "mdns" {
		// A manual private subnet can use normal kernel routing even if NAS
		// interface enumeration failed, or the destination is on another VLAN.
		adapters = []Adapter{{}}
		for _, a := range interfaces {
			if _, _, err := scanRange(a, requested); err == nil {
				adapters = []Adapter{a}
				break
			}
		}
	}
	if len(adapters) == 0 {
		return nil, failure("未读取到局域网接口，请在高级选项填写扫描网段，或手动添加设备", 422, "invalid_params")
	}
	targets := []discoveryTarget{}
	seen := map[string]bool{}
	for _, a := range adapters {
		target := discoveryTarget{adapter: a}
		if mode != "mdns" {
			var err error
			target.cidr, target.hosts, err = scanRange(a, requested)
			if err != nil {
				return nil, err
			}
			if seen[target.cidr] {
				continue
			}
			seen[target.cidr] = true
		}
		targets = append(targets, target)
	}
	return targets, nil
}

type Discovery struct {
	mu            sync.Mutex
	manager       *Manager
	interfaces    func() []Adapter
	browse        func(context.Context, Adapter) ([]string, string)
	probe         func(context.Context, string, string, int) bool
	port          int
	scan          *discoveryScan
	wg            sync.WaitGroup
	nextRecovery  time.Time
	nextAutomatic time.Time
	closed        bool
}

func newDiscovery(m *Manager) *Discovery {
	return &Discovery{manager: m, interfaces: localInterfaces, browse: browseMDNS, probe: probeHTTP, port: 80}
}
func (d *Discovery) snapshot() Obj { d.mu.Lock(); defer d.mu.Unlock(); return d.snapshotLocked() }
func (d *Discovery) snapshotLocked() Obj {
	interfaces := []Obj{}
	for _, a := range d.interfaces() {
		cidr, _, _ := scanRange(a, "")
		interfaces = append(interfaces, Obj{"id": a.ID, "label": a.Label, "range": cidr})
	}
	out := Obj{"interfaces": interfaces, "state": "idle", "interface": "", "phase": "idle", "mdns": "idle", "mode": "auto", "range": "", "checked": 0, "total": 0, "devices": []Obj{}, "protectedAddresses": []string{}}
	if len(interfaces) > 0 {
		out["interface"] = interfaces[0]["id"]
		out["range"] = interfaces[0]["range"]
	}
	s := d.scan
	if s == nil {
		return out
	}
	out["state"] = s.state
	out["interface"] = s.adapter
	out["phase"] = s.phase
	out["mdns"] = s.mdns
	out["mode"] = s.mode
	out["range"] = s.rangeCIDR
	out["checked"] = s.checked
	out["total"] = s.total
	out["background"] = s.background || s.viewers != nil
	devices := []Obj{}
	d.manager.mu.RLock()
	for id, device := range s.devices {
		device = clone(device)
		e := d.manager.devices[id]
		device["added"] = e != nil
		device["ignored"] = d.manager.ignored[id]
		device["canUpdate"] = e != nil && e.d["method"] != "mqtt" && e.d["address"] != device["address"] && e.r.failures > 0
		devices = append(devices, device)
	}
	d.manager.mu.RUnlock()
	sort.Slice(devices, func(i, j int) bool { return str(devices[i]["id"]) < str(devices[j]["id"]) })
	out["devices"] = devices
	out["protectedAddresses"] = append([]string{}, s.protected...)
	return out
}
func (d *Discovery) start(input Obj) (Obj, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.beginLocked(input, false); err != nil {
		return nil, err
	}
	return d.snapshotLocked(), nil
}

// Called after collection; at most one scan runs, and repeated failures do
// not turn every page refresh into a subnet scan. Interfaces are read anew.
func (d *Discovery) recover() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.manager.ctx.Err() != nil || time.Now().Before(d.nextRecovery) || d.scan != nil && d.scan.state == "scanning" {
		return
	}
	if len(d.manager.lostLAN(3)) == 0 {
		return
	}
	d.nextRecovery = time.Now().Add(2 * time.Minute)
	_ = d.beginLocked(Obj{}, true)
}

func (d *Discovery) beginLocked(input Obj, background bool) error {
	if d.closed || d.manager.ctx.Err() != nil {
		return failure("设备发现服务已停止", 503, "unavailable")
	}
	if d.scan != nil && d.scan.state == "scanning" {
		return nil
	}
	mode := str(valueOr(input["mode"], "auto"))
	if mode != "auto" && mode != "mdns" && mode != "lan" {
		return bad("发现方式无效")
	}
	targets, err := discoveryTargets(d.interfaces(), str(input["interface"]), str(input["cidr"]), mode)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(d.manager.ctx)
	s := &discoveryScan{state: "scanning", phase: mode, mdns: "browsing", mode: mode, devices: map[string]Obj{}, protected: []string{}, ctx: ctx, cancel: cancel}
	s.background = background
	s.recovery = d.manager.lostLAN(1)
	if len(targets) == 1 {
		s.adapter, s.rangeCIDR = targets[0].adapter.ID, targets[0].cidr
	}
	for _, target := range targets {
		s.total += len(target.hosts)
	}
	if mode == "lan" {
		s.mdns = "skipped"
	}
	d.scan = s
	d.nextRecovery = time.Now().Add(2 * time.Minute)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer cancel()
		var methods sync.WaitGroup
		if mode != "lan" {
			methods.Add(1)
			go func() {
				defer methods.Done()
				state := "unavailable"
				for _, target := range targets {
					if ctx.Err() != nil {
						break
					}
					addresses, result := d.browse(ctx, target.adapter)
					if result == "complete" {
						state = "complete"
						d.verifyCandidates(s, target.adapter, addresses, "mdns")
					}
				}
				d.mu.Lock()
				s.mdns = state
				d.mu.Unlock()
			}()
		}
		if mode != "mdns" {
			for _, target := range targets {
				if ctx.Err() != nil {
					break
				}
				d.verifyCandidates(s, target.adapter, target.hosts, "lan")
			}
		}
		methods.Wait()
		d.mu.Lock()
		defer d.mu.Unlock()
		s.phase = "done"
		s.state = "complete"
		if ctx.Err() != nil {
			s.state = "cancelled"
		} else if mode == "mdns" && s.mdns == "unavailable" {
			s.state = "unavailable"
		}
	}()
	return nil
}

func (d *Discovery) verifyCandidates(s *discoveryScan, adapter Adapter, hosts []string, mode string) {
	ctx := s.ctx
	jobs := make(chan string)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for host := range jobs {
				if ctx.Err() != nil {
					return
				}
				address := host
				if mode == "lan" {
					if !d.probe(ctx, host, adapter.Address, d.port) {
						d.mu.Lock()
						s.checked++
						d.mu.Unlock()
						continue
					}
					address = "http://" + host
					if d.port != 80 {
						address += fmt.Sprintf(":%d", d.port)
					}
				}
				d.verify(s, address, mode)
				if mode == "lan" {
					d.mu.Lock()
					s.checked++
					d.mu.Unlock()
				}
			}
		}()
	}
send:
	for i, host := range hosts {
		if mode == "mdns" && i >= 64 {
			break
		}
		select {
		case <-ctx.Done():
			break send
		case jobs <- host:
		}
	}
	close(jobs)
	workers.Wait()
}
func (d *Discovery) verify(s *discoveryScan, address, source string) {
	info, err := readIdentity(s.ctx, address, "", 1500*time.Millisecond)
	if s.ctx.Err() != nil {
		return
	}
	if err != nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		if codeOf(err) == "auth_failed" && !contains(s.protected, address) {
			s.protected = append(s.protected, address)
		}
		return
	}
	key, err := identity(str(info["model"]), str(info["device_id"]))
	if err != nil {
		return
	}
	key["address"] = address
	key["source"] = source
	version := str(info["version"])
	if len(version) > 80 {
		version = version[:80]
	}
	key["version"] = version
	id := str(key["id"])
	key["recovered"] = d.manager.recoverAddress(s.ctx, s.recovery[id], info, address)
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	// mDNS and HTTP can find the same identity in parallel. Keep the recovered
	// address and result instead of replacing them with a stale announcement.
	if previous := s.devices[id]; !boolean(previous["recovered"]) {
		s.devices[id] = key
	}
}
func (d *Discovery) cancel() Obj {
	d.mu.Lock()
	if d.scan != nil && d.scan.state == "scanning" && !d.scan.background && d.scan.viewers == nil {
		d.scan.cancel()
		d.scan.state = "cancelled"
	}
	d.mu.Unlock()
	return d.snapshot()
}
func (d *Discovery) close() {
	d.mu.Lock()
	d.closed = true
	if d.scan != nil {
		d.scan.cancel()
	}
	d.mu.Unlock()
	d.wg.Wait()
}
