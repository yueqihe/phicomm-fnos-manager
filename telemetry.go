package main

import (
	"context"
	"encoding/json"
	"math"
	"time"
)

const sampleInterval = int64(time.Minute / time.Millisecond)
const sampleRetention = int64(30 * 24 * time.Hour / time.Millisecond)

type dataMetric struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Unit   string `json:"unit"`
	Digits int    `json:"digits"`
	Total  bool   `json:"total"`
}

func dataMetrics(model string) []dataMetric {
	switch model {
	case "TC1", "S30-V1B":
		metrics := []dataMetric{{"power", "功率", "W", 1, false}, {"energy", "用电量", "度", 3, true}, {"cost", "预估花费", "元", 2, true}}
		if model == "S30-V1B" {
			metrics = append(metrics, dataMetric{"voltage", "电压", "V", 1, false}, dataMetric{"current", "电流", "A", 3, false})
		}
		return metrics
	case "M1":
		return []dataMetric{{"pm25", "PM2.5", "μg/m³", 0, false}, {"hcho", "甲醛", "mg/m³", 3, false}, {"temp", "温度", "°C", 1, false}, {"humidity", "湿度", "%", 1, false}, {"co2", "CO₂", "ppm", 0, false}}
	case "DAIKIN":
		return []dataMetric{{"pm25", "PM2.5", "μg/m³", 0, false}, {"pm10", "PM10", "μg/m³", 0, false}, {"co2", "CO₂", "ppm", 0, false}, {"temp", "温度", "°C", 1, false}, {"humidity", "湿度", "%", 1, false}, {"tvoc", "TVOC", "ppb", 0, false}, {"hcho", "甲醛", "mg/m³", 3, false}}
	case "A1":
		return []dataMetric{{"speed", "风机输出", "%", 0, false}, {"filter", "滤芯剩余", "%", 0, false}}
	}
	return nil
}

func chartMetric(d Obj) string {
	metrics := dataMetrics(str(d["model"]))
	for _, metric := range metrics {
		if metric.ID == str(d["chartMetric"]) {
			return metric.ID
		}
	}
	if len(metrics) > 0 {
		return metrics[0].ID
	}
	return ""
}

// Only fresh successful reads enter history. Cached/offline status is never sampled.
func (s *Store) recordSample(d Obj, at int64) {
	state, values := obj(d["state"]), Obj{}
	put := func(key string, value any) { values[key] = finite(value) }
	switch d["model"] {
	case "TC1", "S30-V1B":
		put("power", state["power"])
		if d["model"] == "S30-V1B" {
			put("voltage", state["voltage"])
			put("current", state["current"])
		}
		if n := finite(state["energy"]); n != nil && num(n) >= 0 {
			put("meter", n)
		}
		bill := obj(d["billing"])
		if values["meter"] != nil && bill["since"] != nil && bill["lastAt"] != nil {
			put("costTotal", bill["cost"])
		}
	case "M1":
		if boolean(state["sensorValid"]) {
			for _, key := range []string{"pm25", "hcho", "temp", "humidity"} {
				put(key, state[key])
			}
		}
		put("co2", state["co2"])
	case "DAIKIN":
		for _, key := range []string{"pm25", "pm10", "co2", "temp", "humidity", "tvoc", "hcho"} {
			put(key, state[key])
		}
	case "A1":
		put("speed", state["appliedSpeed"])
		put("filter", state["filter"])
	default:
		return
	}
	raw, err := json.Marshal(values)
	must(err)
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	// One snapshot per minute, shared by LAN and MQTT and preserved across restarts.
	_, err = s.db.Exec("INSERT OR IGNORE INTO telemetry(device_id,minute,at,value) VALUES (?,?,?,?)", d["id"], at/sampleInterval, at, string(raw))
	must(err)
	if at-s.lastPrune >= int64(time.Hour/time.Millisecond) {
		s.pruneSamples(at)
	}
}

func (s *Store) pruneSamples(at int64) {
	_, err := s.db.Exec("DELETE FROM telemetry WHERE at < ?", at-sampleRetention)
	must(err)
	_, err = s.db.Exec("DELETE FROM telemetry_hours WHERE hour < ?", (at-sampleRetention)/historyHour*historyHour)
	must(err)
	s.lastPrune = at
}

func (s *Store) maintainSamples(at int64) {
	s.dataMu.Lock()
	defer s.dataMu.Unlock()
	if at-s.lastPrune >= int64(time.Hour/time.Millisecond) {
		s.pruneSamples(at)
	}
}

type dataStats struct {
	Count int     `json:"n"`
	Sum   float64 `json:"s"`
	Min   float64 `json:"l"`
	Max   float64 `json:"h"`
	Last  float64 `json:"v"`
}

func (s *dataStats) add(value any) {
	n, ok := finite(value).(float64)
	if !ok {
		return
	}
	if s.Count == 0 {
		s.Min, s.Max = n, n
	}
	s.Count++
	s.Sum += n
	s.Min = math.Min(s.Min, n)
	s.Max = math.Max(s.Max, n)
	s.Last = n
}
func (s dataStats) public() Obj {
	if s.Count == 0 {
		return Obj{"count": 0, "min": nil, "max": nil, "avg": nil, "total": nil, "latest": nil}
	}
	return Obj{"count": s.Count, "min": s.Min, "max": s.Max, "avg": s.Sum / float64(s.Count), "total": s.Sum, "latest": s.Last}
}

func (s *Store) rawDataHistory(ctx context.Context, id, model, span string, now int64) (Obj, error) {
	duration, step := int64(0), int64(0)
	switch span {
	case "24h":
		duration, step = 24*60*sampleInterval, 5*sampleInterval
	case "7d":
		duration, step = 7*24*60*sampleInterval, 60*sampleInterval
	case "30d":
		duration, step = sampleRetention, 3*60*sampleInterval
	default:
		return nil, bad("请选择最近 24 小时、7 天或 30 天")
	}
	from := now - duration
	metrics := dataMetrics(model)
	start := from / step * step
	buckets := make([]map[string]*dataStats, int((now-start)/step)+1)
	for i := range buckets {
		buckets[i] = map[string]*dataStats{}
		for _, metric := range metrics {
			buckets[i][metric.ID] = &dataStats{}
		}
	}
	summary := map[string]*dataStats{}
	for _, metric := range metrics {
		summary[metric.ID] = &dataStats{}
	}
	rows, err := s.readDB.QueryContext(ctx, "SELECT at,value FROM telemetry WHERE device_id=? AND minute>=? AND minute<=? AND at>=? AND at<=? ORDER BY minute", id, from/sampleInterval, now/sampleInterval, from, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	previous := Obj{}
	count, first, last := 0, int64(0), int64(0)
	for rows.Next() {
		var at int64
		var raw string
		var values Obj
		if err = rows.Scan(&at, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, err
		}
		if count == 0 {
			first = at
		}
		last = at
		count++
		// Counter differences use only actual readings within the selected period.
		// Meter resets establish a new baseline; no negative usage or invented history.
		if meteredPlug(model) {
			for key, counter := range map[string]string{"energy": "meter", "cost": "costTotal"} {
				a, aOK := finite(previous[counter]).(float64)
				b, bOK := finite(values[counter]).(float64)
				if aOK && bOK && b >= a {
					values[key] = b - a
				}
				if bOK {
					previous[counter] = b
				}
			}
		}
		bucket := buckets[(at-start)/step]
		for _, metric := range metrics {
			bucket[metric.ID].add(values[metric.ID])
			summary[metric.ID].add(values[metric.ID])
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	points := []Obj{}
	for i, bucket := range buckets {
		values := Obj{}
		for _, metric := range metrics {
			stat := bucket[metric.ID]
			var value any
			if stat.Count > 0 {
				value = stat.Sum
				if !metric.Total {
					value = stat.Sum / float64(stat.Count)
				}
			}
			values[metric.ID] = value
		}
		points = append(points, Obj{"time": start + int64(i)*step, "values": values})
	}
	stats := Obj{}
	for key, stat := range summary {
		stats[key] = stat.public()
	}
	var recordedFrom, recordedTo any
	if count > 0 {
		recordedFrom, recordedTo = first, last
	}
	return Obj{"deviceId": id, "range": span, "from": from, "to": now, "stepSeconds": step / 1000, "sampleSeconds": sampleInterval / 1000, "retentionDays": 30, "samples": count, "recordedFrom": recordedFrom, "recordedTo": recordedTo, "metrics": metrics, "summary": stats, "points": points}, nil
}

func (m *Manager) dataHistory(ctx context.Context, id, span string) (Obj, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	model := str(e.d["model"])
	m.mu.RUnlock()
	if span == "" {
		span = "24h"
	}
	return m.store.dataHistory(ctx, id, model, span, time.Now().UnixMilli())
}
