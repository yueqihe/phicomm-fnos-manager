package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Call with m.mu held, except while constructing a manager.
func (m *Manager) refreshSettings() Obj {
	return Obj{"pageSeconds": m.pageSeconds, "pollSeconds": m.interval.Seconds()}
}

func (m *Manager) pollInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

func validateRefresh(input, current Obj) (Obj, error) {
	next := clone(current)
	for _, field := range []struct {
		key, label string
		min, max   float64
	}{{"pageSeconds", "页面刷新间隔", 2, 300}, {"pollSeconds", "局域网采集间隔", 5, 60}} {
		value, present := input[field.key]
		if !present {
			continue
		}
		var n float64
		switch v := value.(type) {
		case float64:
			n = v
		case string:
			var err error
			n, err = strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, bad(field.label + "须填写整数秒")
			}
		default:
			return nil, bad(field.label + "须填写整数秒")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < field.min || n > field.max {
			return nil, bad(fmt.Sprintf("%s须为 %.0f–%.0f 秒的整数", field.label, field.min, field.max))
		}
		next[field.key] = n
	}
	return next, nil
}

func (m *Manager) saveRefresh(input Obj) (Obj, error) {
	m.mu.Lock()
	next, err := validateRefresh(input, m.refreshSettings())
	if err == nil {
		m.store.set("refresh", next)
		poll := time.Duration(num(next["pollSeconds"])) * time.Second
		changed := poll != m.interval
		m.interval, m.pageSeconds = poll, num(next["pageSeconds"])
		if changed {
			select {
			case m.pollChanged <- struct{}{}:
			default:
			}
		}
		m.store.log(nil, "refresh.configure", "ok", "刷新时间已保存")
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return m.settings(), nil
}
