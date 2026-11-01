package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAutomaticDiscoveryIsPageDrivenSharedAndThrottled(t *testing.T) {
	m, a, server := testManager(t)
	f := firmwareFixture(t, "M1", "aabbcc000097")
	d := a.discovery
	discoveryFixture(d, f.server.URL)
	first, second := token(), token()
	path := "discovery/automatic?client=" + first
	if status, _ := requestJSON(t, server, path, "GET", nil); status != 200 || d.scan != nil {
		t.Fatal("polling started a scan without an open-page request")
	}
	if status, data := requestJSON(t, server, path, "POST", Obj{}); status != 200 {
		t.Fatal(status, data)
	}
	d.wg.Wait()
	scan := d.scan
	if scan == nil || scan.viewers == nil || scan.state != "complete" || len(m.list()) != 0 {
		t.Fatal("discovery registered a device without confirmation")
	}
	for _, client := range []string{first, second} {
		snapshot, err := d.automatic(client, "start")
		if err != nil || d.scan != scan || len(snapshot["devices"].([]Obj)) != 1 {
			t.Fatal("multiple pages did not share a recent scan", snapshot, err)
		}
		device := snapshot["devices"].([]Obj)[0]
		if device["added"] != false || device["model"] != "M1" {
			t.Fatal("new device not offered for confirmation", device)
		}
	}
	d.nextAutomatic = time.Time{}
	if _, err := d.automatic(first, "start"); err != nil {
		t.Fatal(err)
	}
	d.wg.Wait()
	if d.scan == scan {
		t.Fatal("next minute did not start a fresh scan")
	}
	// Merely polling after a minute (or closing a page) cannot schedule more scans.
	d.nextAutomatic = time.Time{}
	scan = d.scan
	d.automatic(first, "poll")
	d.automatic(first, "leave")
	if d.scan != scan {
		t.Fatal("scan continued without an explicit page timer request")
	}
	for _, invalid := range []string{"", "bad", strings.Repeat("z", 32), strings.Repeat("a", 34)} {
		if status, _ := requestJSON(t, server, "discovery/automatic?client="+invalid, "POST", Obj{}); status != 400 {
			t.Fatal("invalid client accepted", status)
		}
	}
}

func TestAutomaticDiscoveryStopsWhenLastPageLeavesOrExpires(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "leave", true: "lost-connection"}[expire], func(t *testing.T) {
			_, a, _ := testManager(t)
			d := a.discovery
			discoveryFixture(d)
			started := make(chan struct{})
			d.browse = func(ctx context.Context, _ Adapter) ([]string, string) {
				close(started)
				<-ctx.Done()
				return nil, "unavailable"
			}
			first, second := token(), token()
			d.automatic(first, "start")
			<-started
			d.automatic(second, "start")
			if d.cancel()["state"] != "scanning" {
				t.Fatal("closing manual add dialog cancelled page discovery")
			}
			d.automatic(first, "leave")
			if d.scan.ctx.Err() != nil {
				t.Fatal("one page cancelled another page's scan")
			}
			if expire {
				d.mu.Lock()
				d.scan.viewers[second] = time.Now().Add(-time.Second)
				d.expireViewersLocked(d.scan, time.Now())
				d.mu.Unlock()
			} else {
				d.automatic(second, "leave")
			}
			d.wg.Wait()
			if d.scan.state != "cancelled" {
				t.Fatal("orphaned scan was not stopped")
			}
		})
	}
}

func TestAutomaticDiscoveryRenewsOnlyRegisteredViewerAndKeepsOtherScans(t *testing.T) {
	_, a, _ := testManager(t)
	d := a.discovery
	discoveryFixture(d)
	d.browse = func(ctx context.Context, _ Adapter) ([]string, string) {
		<-ctx.Done()
		return nil, "unavailable"
	}
	client := token()
	d.automatic(client, "start")
	d.mu.Lock()
	d.scan.viewers[client] = time.Now().Add(time.Second)
	d.mu.Unlock()
	d.automatic(client, "poll")
	d.automatic(token(), "poll")
	d.mu.Lock()
	valid := len(d.scan.viewers) == 1 && time.Until(d.scan.viewers[client]) > 10*time.Second
	d.mu.Unlock()
	if !valid {
		t.Fatal("poll did not renew only its own lease")
	}
	d.automatic(client, "leave")
	d.wg.Wait()
	// A page opening/closing during manual discovery must not take ownership.
	if _, err := d.start(Obj{}); err != nil {
		t.Fatal(err)
	}
	d.automatic(client, "start")
	d.automatic(client, "leave")
	if d.scan.viewers != nil || d.scan.ctx.Err() != nil {
		t.Fatal("page lifecycle interfered with manual discovery")
	}
	d.cancel()
	d.wg.Wait()
	// The existing offline-device recovery is also independent of page lifetime.
	d.mu.Lock()
	if err := d.beginLocked(Obj{}, true); err != nil {
		t.Fatal(err)
	}
	d.mu.Unlock()
	d.automatic(client, "start")
	d.automatic(client, "leave")
	if d.scan.viewers != nil || d.scan.ctx.Err() != nil {
		t.Fatal("page lifecycle cancelled offline device recovery")
	}
	d.close()
}

func TestAutomaticDiscoveryIdentifiesRegisteredAndRemovedDevices(t *testing.T) {
	m, a, _ := testManager(t)
	f, e := recoveryFixture(t, m, "M1", "lan")
	d := a.discovery
	discoveryFixture(d, f.server.URL)
	client := token()
	d.automatic(client, "start")
	d.wg.Wait()
	snapshot, _ := d.automatic(client, "poll")
	if device := snapshot["devices"].([]Obj)[0]; device["added"] != true || device["ignored"] != false {
		t.Fatal("registered device was treated as new", device)
	}
	m.remove(str(e.d["id"]))
	snapshot, _ = d.automatic(client, "poll")
	if device := snapshot["devices"].([]Obj)[0]; device["added"] != false || device["ignored"] != true {
		t.Fatal("removed device was offered again", device)
	}
}
