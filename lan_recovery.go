package main

import (
	"context"
	"fmt"
)

type lanRecoveryTarget struct {
	entry  *deviceEntry
	config Obj
}

// Capture the original connection so a late scan cannot overwrite a manual
// edit, a successful reconnection, or a removed/re-added entry.
func (m *Manager) lostLAN(minFailures int) map[string]lanRecoveryTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targets := map[string]lanRecoveryTarget{}
	if m.closed {
		return targets
	}
	for id, e := range m.devices {
		if e.d["method"] != "mqtt" && str(e.d["address"]) != "" && e.r.failures >= minFailures {
			targets[id] = lanRecoveryTarget{entry: e, config: clone(e.d)}
		}
	}
	return targets
}

func (m *Manager) recoveryCurrent(target lanRecoveryTarget) bool {
	e, old := target.entry, target.config
	if m.closed || m.devices[str(old["id"])] != e || e.r.failures == 0 {
		return false
	}
	for _, field := range []string{"address", "method", "password"} {
		if e.d[field] != old[field] {
			return false
		}
	}
	return true
}

func (m *Manager) recoverAddress(ctx context.Context, target lanRecoveryTarget, info Obj, address string) bool {
	e := target.entry
	if e == nil || ctx.Err() != nil || verifyIdentity(info, target.config) != nil {
		return false
	}
	address, err := lanURL(address)
	if err != nil || address == target.config["address"] || !e.op.TryLock() {
		return false
	}
	defer e.op.Unlock()
	m.mu.RLock()
	valid := m.recoveryCurrent(target)
	next := clone(e.d)
	m.mu.RUnlock()
	if !valid {
		return false
	}
	next["address"] = address
	// Discovery already identified the device without credentials. Only now
	// may its saved password be used, and a full read must succeed before commit.
	state, actions, err := m.readDevice(ctx, next, "lan")
	if err != nil || ctx.Err() != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.recoveryCurrent(target) || ctx.Err() != nil {
		return false
	}
	e.d["address"] = address
	m.applyReading(e, state, actions, "lan")
	m.store.log(e.d, "connection.recover", "ok", fmt.Sprintf("设备地址已自动更新：%s → %s", target.config["address"], address))
	return true
}
