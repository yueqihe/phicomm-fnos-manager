package main

import (
	"encoding/hex"
	"time"
)

const discoveryViewerTTL = 15 * time.Second

// Automatic discovery is driven by open pages, never by a server timer.
// Pages share one scan and renew a short lease while receiving its results.
func (d *Discovery) automatic(client, action string) (Obj, error) {
	if raw, err := hex.DecodeString(client); err != nil || len(raw) != 16 {
		return nil, bad("扫描会话无效")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.manager.ctx.Err() != nil {
		return nil, failure("设备发现服务已停止", 503, "unavailable")
	}
	now := time.Now()
	s := d.scan
	if action == "start" && (s == nil || s.state != "scanning") && !now.Before(d.nextAutomatic) {
		if err := d.beginLocked(Obj{}, false); err != nil {
			// Also bound retries when several open pages cannot read an interface.
			d.nextAutomatic = now.Add(time.Minute)
			return nil, err
		}
		s = d.scan
		s.viewers = map[string]time.Time{}
		d.nextAutomatic = now.Add(time.Minute)
		d.wg.Add(1)
		go d.watchViewers(s)
	}
	if s != nil && s.state == "scanning" && s.viewers != nil {
		switch action {
		case "start":
			s.viewers[client] = now.Add(discoveryViewerTTL)
		case "poll":
			if _, exists := s.viewers[client]; exists {
				s.viewers[client] = now.Add(discoveryViewerTTL)
			}
		case "leave":
			delete(s.viewers, client)
		}
		d.expireViewersLocked(s, now)
	}
	return d.snapshotLocked(), nil
}

func (d *Discovery) expireViewersLocked(s *discoveryScan, now time.Time) {
	for client, until := range s.viewers {
		if !now.Before(until) {
			delete(s.viewers, client)
		}
	}
	if s.state == "scanning" && len(s.viewers) == 0 {
		s.cancel()
	}
}

func (d *Discovery) watchViewers(s *discoveryScan) {
	defer d.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			d.mu.Lock()
			d.expireViewersLocked(s, now)
			d.mu.Unlock()
		}
	}
}
