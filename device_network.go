package main

import (
	"net"
	"unicode/utf8"
)

// Keep only public connection details; never copy credentials from device status.
func networkState(status Obj) Obj {
	network := Obj{"ssid": nil, "rssi": nil, "ip": nil, "mask": nil, "gateway": nil, "connected": nil}
	if ssid, ok := status["ssid"].(string); ok && len(ssid) <= 128 && utf8.ValidString(ssid) {
		network["ssid"] = ssid
	}
	for _, key := range []string{"ip", "mask", "gateway"} {
		if ip := net.ParseIP(str(status[key])); ip != nil {
			network[key] = ip.String()
		}
	}
	for _, key := range []string{"rssi", "wifi_rssi"} {
		if value := finite(status[key]); value != nil && num(value) >= -127 && num(value) <= 0 {
			network["rssi"] = value
			break
		}
	}
	if connected, ok := status["wifi_connected"].(bool); ok {
		network["connected"] = connected
	}
	return network
}
