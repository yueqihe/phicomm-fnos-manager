package main

import (
	"context"
	"sync"
	"time"
)

const historyCacheLimit = 64

type historyKey struct {
	id, model, span  string
	minute, revision int64
}

type historyCached struct {
	data Obj
	at   time.Time
}

type historyCache struct {
	mu      sync.Mutex
	entries map[historyKey]historyCached
	loading map[historyKey]chan struct{}
}

// Callers receive private maps/slices; encoding concurrent HTTP responses or
// editing a response cannot mutate a cached chart shared by other requests.
func copyHistory(value any) any {
	switch v := value.(type) {
	case Obj:
		out := make(Obj, len(v))
		for key, item := range v {
			out[key] = copyHistory(item)
		}
		return out
	case []Obj:
		out := make([]Obj, len(v))
		for i, item := range v {
			out[i] = copyHistory(item).(Obj)
		}
		return out
	case []dataMetric:
		return append([]dataMetric(nil), v...)
	default:
		return v
	}
}

func (s *Store) dataHistory(ctx context.Context, id, model, span string, now int64) (Obj, error) {
	if span != "24h" && span != "7d" && span != "30d" {
		return nil, bad("请选择最近 24 小时、7 天或 30 天")
	}
	var revision int64
	// Triggers also cover pruning, corrections and external imports. A new sample
	// invalidates cached charts immediately, including samples in the same minute.
	if err := s.readDB.QueryRowContext(ctx, "SELECT COALESCE((SELECT revision FROM telemetry_revision WHERE device_id=?),0)", id).Scan(&revision); err != nil {
		return nil, err
	}
	key := historyKey{id, model, span, now / sampleInterval, revision}
	c := &s.charts
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if entry, ok := c.entries[key]; ok && time.Since(entry.at) < time.Minute {
			c.mu.Unlock()
			return copyHistory(entry.data).(Obj), nil
		}
		if done, ok := c.loading[key]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if c.loading == nil {
			c.loading = make(map[historyKey]chan struct{})
			c.entries = make(map[historyKey]historyCached)
		}
		done := make(chan struct{})
		c.loading[key] = done
		c.mu.Unlock()

		var data Obj
		var err error
		if span == "24h" {
			data, err = s.rawDataHistory(ctx, id, model, span, now)
		} else {
			data, err = s.hourlyDataHistory(ctx, id, model, span, now)
		}
		c.mu.Lock()
		if err == nil {
			for k, entry := range c.entries {
				if time.Since(entry.at) >= time.Minute || (k.id == id && k.revision != revision) {
					delete(c.entries, k)
				}
			}
			if len(c.entries) >= historyCacheLimit {
				var oldest historyKey
				var at time.Time
				for k, entry := range c.entries {
					if at.IsZero() || entry.at.Before(at) {
						oldest, at = k, entry.at
					}
				}
				delete(c.entries, oldest)
			}
			c.entries[key] = historyCached{data, time.Now()}
		}
		delete(c.loading, key)
		close(done)
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return copyHistory(data).(Obj), nil
	}
}
