package main

import (
	"context"
	"encoding/json"
)

// Frozen pre-optimization raw query: an independent oracle for rollup equivalence.
func (s *Store) referenceDataHistory(ctx context.Context, id, model, span string, now int64) (Obj, error) {
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
	rows, err := s.db.QueryContext(ctx, "SELECT at,value FROM telemetry WHERE device_id=? AND minute>=? AND minute<=? AND at>=? AND at<=? ORDER BY minute", id, from/sampleInterval, now/sampleInterval, from, now)
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
