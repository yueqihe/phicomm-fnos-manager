package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UDP connect only consults the routing table; it sends no packet without Write.
func routeSourceIP(target net.IP) net.IP {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: target, Port: 9})
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP
}

// /proc remains usable on NAS systems that restrict interface enumeration via
// netlink. Only directly connected LAN routes are candidates for auto scanning.
func routeInterfaces(table string, source func(net.IP) net.IP, index func(string) int) []Adapter {
	out := []Adapter{}
	seen := map[string]bool{}
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || strings.ContainsAny(fields[0], `/\`) || fields[0] == ".." || !usableInterface(net.Interface{Name: fields[0], Flags: net.FlagUp}) {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&1 == 0 || flags&2 != 0 {
			continue
		}
		decode := func(value string) net.IP {
			n, err := strconv.ParseUint(value, 16, 32)
			if err != nil || len(value) != 8 {
				return nil
			}
			return net.IPv4(byte(n), byte(n>>8), byte(n>>16), byte(n>>24)).To4()
		}
		destination, maskIP := decode(fields[1]), decode(fields[7])
		if destination == nil || maskIP == nil || !privateIP(destination) || destination.IsLoopback() {
			continue
		}
		mask := net.IPMask(maskIP)
		bits, size := mask.Size()
		if bits == 0 || size != 32 {
			continue
		}
		network := &net.IPNet{IP: destination.Mask(mask), Mask: mask}
		target := network.IP
		if bits < 31 {
			target = numberIP(ipNumber(target) + 1)
		}
		ip := source(target)
		if ip == nil || ip.To4() == nil || !privateIP(ip) || !network.Contains(ip) || seen[ip.String()] {
			continue
		}
		address := ip.String()
		seen[address] = true
		out = append(out, Adapter{ID: address, Label: fields[0] + " · " + address, Address: address, CIDR: fmt.Sprintf("%s/%d", address, bits), Index: index(fields[0])})
	}
	return out
}

func fallbackInterfaces() []Adapter {
	table, _ := os.ReadFile("/proc/net/route")
	out := routeInterfaces(string(table), routeSourceIP, func(name string) int {
		raw, _ := os.ReadFile(filepath.Join("/sys/class/net", name, "ifindex"))
		n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		return n
	})
	if len(out) == 0 {
		ip := routeSourceIP(net.IPv4(192, 0, 2, 1))
		if ip != nil && ip.To4() != nil && privateIP(ip) && !ip.IsLoopback() {
			address := ip.String()
			out = append(out, Adapter{ID: address, Label: "自动路由 · " + address, Address: address, CIDR: address + "/24"})
		}
	}
	return out
}
