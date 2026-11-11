package main

import (
	"fmt"
	"reflect"
	"strings"
)

// Keep the last observed values separately from current readings: an omitted
// field is not a change, and restarting the manager must not repeat events.
func activityValues(state Obj) Obj {
	values := Obj{}
	for _, key := range []string{"ip", "ssid", "gateway"} {
		if value := str(obj(state["network"])[key]); value != "" {
			values[key] = value
		}
	}
	for _, key := range []string{"light", "childLock", "mode", "brightness", "powerOnMode"} {
		if value := state[key]; value != nil {
			values[key] = value
		}
	}
	if _, plug := state["outlets"]; !plug {
		if on, valid := state["on"].(bool); valid {
			values["fanOn"] = on
		}
	}
	for i, value := range arr(state["outlets"]) {
		if _, valid := value.(bool); valid {
			values[fmt.Sprintf("outlet:%d", i)] = value
		}
	}
	return values
}

func outletLabel(d Obj, index int) string {
	if d["model"] == "S30-V1B" {
		return "插座"
	}
	label := fmt.Sprintf("插口 %d", index+1)
	if names := arr(obj(d["state"])["outletNames"]); index < len(names) && str(names[index]) != "" {
		label += "（" + str(names[index]) + "）"
	}
	return label
}

func switchWord(value any) string {
	if boolean(value) {
		return "开启"
	}
	return "关闭"
}

func powerOnLabel(value any) string {
	return map[string]string{"off": "上电关闭", "on": "上电开启", "restore": "保持断电前状态"}[str(value)]
}

func modeLabel(value any) string {
	if value == "auto" {
		return "自动"
	}
	if value == "manual" {
		return "手动"
	}
	return str(value)
}

// Only suppress the exact value requested by the in-flight command. Changes
// to other outlets/settings arriving over MQTT are still recorded.
func activityTargets(d Obj, action string, p Obj) Obj {
	values := Obj{}
	switch action {
	case "outlet.set":
		if index, exists := p["index"]; exists {
			values[fmt.Sprintf("outlet:%d", int(num(index)))] = p["on"]
		} else {
			for i := 0; i < socketCount(str(d["model"])); i++ {
				values[fmt.Sprintf("outlet:%d", i)] = p["on"]
			}
		}
	case "light.set", "indicator.set":
		values["light"] = p["enabled"]
	case "child_lock.set":
		values["childLock"] = p["enabled"]
	case "display.set":
		values["brightness"] = p["brightness"]
	case "fan.mode.set":
		values["mode"] = p["mode"]
	case "fan.set":
		if value, present := p["on"]; present {
			values["fanOn"] = value
		}
		if value, present := p["mode"]; present {
			values["mode"] = value
		}
		if value, present := p["speed"]; present {
			values["fanOn"] = num(value) > 0
		}
	case "config.set":
		if p["section"] == "device" {
			values["powerOnMode"] = requestedPowerOnMode(obj(p["values"]))
		}
	}
	return values
}

func controlDescription(d Obj, action string, p Obj) string {
	switch action {
	case "outlet.set":
		label := "全部插口"
		if d["model"] == "S30-V1B" {
			label = "插座"
		} else if index, exists := p["index"]; exists {
			label = outletLabel(d, int(num(index)))
		}
		return label + "：" + switchWord(p["on"])
	case "light.set", "indicator.set":
		return "指示灯：" + switchWord(p["enabled"])
	case "child_lock.set":
		return "童锁：" + switchWord(p["enabled"])
	case "display.set":
		return fmt.Sprintf("屏幕亮度：%g%%", num(p["brightness"]))
	case "fan.mode.set":
		return "风机模式：" + modeLabel(p["mode"])
	case "fan.set":
		parts := []string{}
		if value, present := p["on"]; present {
			parts = append(parts, "风机："+switchWord(value))
		}
		if value, present := p["mode"]; present {
			parts = append(parts, "模式："+modeLabel(value))
		}
		if value, present := p["speed"]; present {
			parts = append(parts, fmt.Sprintf("风机设定：%g%%", num(value)))
		}
		return strings.Join(parts, "，")
	case "config.set":
		values := obj(p["values"])
		if p["section"] == "device" {
			return "上电输出：" + powerOnLabel(requestedPowerOnMode(values))
		}
		if p["section"] == "socket" {
			return fmt.Sprintf("插口 %d 重命名为“%s”", int(num(values["index"]))+1, str(values["name"]))
		}
		if p["section"] == "mqtt" {
			return "写入 MQTT 连接配置"
		}
	}
	return "设备操作"
}

// Caller holds m.mu. Sensor samples and RSSI fluctuations are intentionally
// excluded; only meaningful connection/configuration changes enter history.
func (m *Manager) recordStateChanges(e *deviceEntry, state Obj) {
	previous := obj(e.d["activity"])
	for key, value := range activityValues(state) {
		old, known := previous[key]
		previous[key] = value
		if !known || reflect.DeepEqual(old, value) || reflect.DeepEqual(e.r.activityTargets[key], value) {
			continue
		}
		action, message := "", ""
		switch key {
		case "ip", "ssid", "gateway":
			label := map[string]string{"ip": "IP 地址", "ssid": "Wi-Fi", "gateway": "默认网关"}[key]
			action = map[string]string{"ip": "network.ip", "ssid": "network.wifi", "gateway": "network.gateway"}[key]
			message = fmt.Sprintf("%s 变更：%s → %s", label, old, value)
		case "light", "childLock", "fanOn":
			label := map[string]string{"light": "指示灯", "childLock": "童锁", "fanOn": "风机"}[key]
			action = map[string]string{"light": "indicator.change", "childLock": "child_lock.change", "fanOn": "fan.change"}[key]
			message = label + "：" + switchWord(old) + " → " + switchWord(value)
		case "mode":
			action, message = "fan.change", "风机模式："+modeLabel(old)+" → "+modeLabel(value)
		case "brightness":
			action, message = "display.change", fmt.Sprintf("屏幕亮度：%g%% → %g%%", num(old), num(value))
		case "powerOnMode":
			action, message = "power_on.change", "上电输出："+powerOnLabel(old)+" → "+powerOnLabel(value)
		default:
			var index int
			if _, err := fmt.Sscanf(key, "outlet:%d", &index); err == nil {
				action, message = "outlet.change", outletLabel(e.d, index)+"："+switchWord(old)+" → "+switchWord(value)
			}
		}
		if action != "" {
			m.store.log(e.d, action, "observed", message)
		}
	}
	e.d["activity"] = previous
}

// Online means at least one usable route. LAN failures must not produce an
// offline event while MQTT remains available. Use the same grace as the UI.
func (m *Manager) recordPresence(e *deviceEntry) bool {
	if m.closed || m.devices[str(e.d["id"])] != e {
		return false
	}
	previous := obj(e.d["activity"])
	online := m.alive(e, "lan") || m.alive(e, "mqtt")
	old, known := previous["online"].(bool)
	if known && old == online {
		return false
	}
	previous["online"] = online
	e.d["activity"] = previous
	if known || online {
		action, message := "device.offline", "设备已离线"
		if online {
			action, message = "device.online", "设备已上线"
		}
		m.store.log(e.d, action, "observed", message)
	}
	return true
}
