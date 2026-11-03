package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
)

var historyFields = []string{"power", "voltage", "current", "pm25", "pm10", "hcho", "temp", "humidity", "co2", "tvoc", "speed", "filter"}
var historyCounters = []struct{ metric, field string }{{"energy", "meter"}, {"cost", "costTotal"}}

type counterEdges struct {
	First float64 `json:"first"`
	Last  float64 `json:"last"`
}

type hourAggregate struct {
	Count    int                      `json:"count"`
	First    int64                    `json:"first"`
	Last     int64                    `json:"last"`
	Stats    map[string]*dataStats    `json:"stats"`
	Counters map[string]*counterEdges `json:"counters"`
}

func (a *hourAggregate) add(at int64, values Obj) {
	if a.Count == 0 {
		a.First = at
		a.Stats = make(map[string]*dataStats)
		a.Counters = make(map[string]*counterEdges)
	}
	a.Count++
	a.Last = at
	add := func(key string, value any) {
		if finite(value) == nil {
			return
		}
		if a.Stats[key] == nil {
			a.Stats[key] = &dataStats{}
		}
		a.Stats[key].add(value)
	}
	for _, key := range historyFields {
		add(key, values[key])
	}
	// Keep first/last valid counters separately. Inter-hour differences are
	// computed only when merging the requested range, never across its start.
	for _, counter := range historyCounters {
		if n, ok := finite(values[counter.field]).(float64); ok {
			edges := a.Counters[counter.metric]
			if edges == nil {
				a.Counters[counter.metric] = &counterEdges{n, n}
			} else {
				if n >= edges.Last {
					add(counter.metric, n-edges.Last)
				}
				edges.Last = n
			}
		}
	}
}

func (s *dataStats) merge(other *dataStats) {
	if other == nil || other.Count == 0 {
		return
	}
	if s.Count == 0 {
		*s = *other
		return
	}
	s.Count += other.Count
	s.Sum += other.Sum
	s.Min = math.Min(s.Min, other.Min)
	s.Max = math.Max(s.Max, other.Max)
	s.Last = other.Last
}

type hourPart struct {
	hour, revision int64
	full, missing  bool
	data           hourAggregate
}

func (s *Store) historyHours(ctx context.Context, id string, from, to int64) ([]hourPart, error) {
	start := from / historyHour * historyHour
	parts := make([]hourPart, (to-start)/historyHour+1)
	for i := range parts {
		hour := start + int64(i)*historyHour
		parts[i] = hourPart{hour: hour, full: hour >= from && hour+historyHour-1 <= to, missing: true}
	}
	// Read summaries and minute records from the same WAL snapshot.
	tx, err := s.readDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT hour,revision,value FROM telemetry_hours WHERE device_id=? AND hour>=? AND hour<=? ORDER BY hour", id, start, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hour, revision int64
		var value sql.NullString
		if err = rows.Scan(&hour, &revision, &value); err != nil {
			rows.Close()
			return nil, err
		}
		part := &parts[(hour-start)/historyHour]
		part.revision = revision
		if part.full && value.Valid {
			if err = json.Unmarshal([]byte(value.String), &part.data); err != nil {
				rows.Close()
				return nil, err
			}
			part.missing = false
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Coalesce missing hours: first access to an old database performs one
	// indexed scan, subsequent requests usually read just the two partial hours.
	for i := 0; i < len(parts); {
		if !parts[i].missing {
			i++
			continue
		}
		j := i + 1
		for j < len(parts) && parts[j].missing {
			j++
		}
		lo, hi := max(from, parts[i].hour), min(to, parts[j-1].hour+historyHour-1)
		rows, err = tx.QueryContext(ctx, "SELECT at,value FROM telemetry WHERE device_id=? AND minute>=? AND minute<=? AND at>=? AND at<=? ORDER BY minute", id, lo/sampleInterval, hi/sampleInterval, lo, hi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var at int64
			var raw string
			var values Obj
			if err = rows.Scan(&at, &raw); err == nil {
				err = json.Unmarshal([]byte(raw), &values)
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			parts[(at-start)/historyHour].data.add(at, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		i = j
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	// Short write batches release the writer between groups of summaries. The
	// revision guard prevents a concurrent sample/prune from being overwritten.
	for offset := 0; offset < len(parts); offset += 24 {
		var updates []hourPart
		for _, part := range parts[offset:min(offset+24, len(parts))] {
			if part.full && part.missing {
				updates = append(updates, part)
			}
		}
		if len(updates) == 0 {
			continue
		}
		if err = s.saveHistoryHours(ctx, id, updates); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

func (s *Store) saveHistoryHours(ctx context.Context, id string, parts []hourPart) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, part := range parts {
		raw, err := json.Marshal(part.data)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO telemetry_hours(device_id,hour,revision,value) VALUES(?,?,?,?)
 ON CONFLICT(device_id,hour) DO UPDATE SET value=excluded.value WHERE telemetry_hours.revision=excluded.revision`, id, part.hour, part.revision, string(raw))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) hourlyDataHistory(ctx context.Context, id, model, span string, now int64) (Obj, error) {
	duration, step := sampleRetention, 3*historyHour
	if span == "7d" {
		duration, step = 7*24*historyHour, historyHour
	}
	from := now - duration
	parts, err := s.historyHours(ctx, id, from, now)
	if err != nil {
		return nil, err
	}
	metrics := dataMetrics(model)
	start := from / step * step
	buckets := make([]map[string]*dataStats, (now-start)/step+1)
	newStats := func() map[string]*dataStats {
		out := make(map[string]*dataStats, len(metrics))
		for _, metric := range metrics {
			out[metric.ID] = &dataStats{}
		}
		return out
	}
	for i := range buckets {
		buckets[i] = newStats()
	}
	summary := newStats()
	previous := make(map[string]float64)
	count, first, last := 0, int64(0), int64(0)
	for _, part := range parts {
		a := part.data
		if a.Count == 0 {
			continue
		}
		if count == 0 {
			first = a.First
		}
		count += a.Count
		last = a.Last
		bucket := buckets[(part.hour-start)/step]
		if meteredPlug(model) {
			for _, counter := range historyCounters {
				if edges := a.Counters[counter.metric]; edges != nil {
					if before, ok := previous[counter.metric]; ok && edges.First >= before {
						bucket[counter.metric].add(edges.First - before)
						summary[counter.metric].add(edges.First - before)
					}
					previous[counter.metric] = edges.Last
				}
			}
		}
		for _, metric := range metrics {
			bucket[metric.ID].merge(a.Stats[metric.ID])
			summary[metric.ID].merge(a.Stats[metric.ID])
		}
	}
	points := make([]Obj, 0, len(buckets))
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
