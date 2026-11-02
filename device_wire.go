package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

const maxDevicePayload = 262144

func encodeDeviceWire(value Obj, binary bool) ([]byte, error) {
	if !binary {
		return json.Marshal(value)
	}
	var out bytes.Buffer
	enc := msgpack.NewEncoder(&out)
	enc.SetSortMapKeys(true)
	enc.UseCompactInts(true)
	enc.UseCompactFloats(true)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func messagePackMap(data []byte) bool {
	return len(data) > 0 && (msgpcode.IsFixedMap(data[0]) || data[0] == msgpcode.Map16 || data[0] == msgpcode.Map32)
}

// Device envelopes use string keys and JSON-compatible values. Bound nested
// containers and normalize numeric types to the existing float64 data model.
func decodeDeviceWire(data []byte) (Obj, bool, error) {
	invalid := fmt.Errorf("invalid device payload")
	if len(data) == 0 || len(data) > maxDevicePayload {
		return nil, false, invalid
	}
	binary := messagePackMap(data)
	if !binary {
		var out Obj
		if err := json.Unmarshal(data, &out); err != nil || out == nil {
			return nil, false, invalid
		}
		return out, false, nil
	}
	dec := msgpack.NewDecoder(bytes.NewReader(data))
	budget := 16384
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		budget--
		if depth > 32 || budget < 0 {
			return nil, invalid
		}
		code, err := dec.PeekCode()
		if err != nil {
			return nil, err
		}
		switch {
		case msgpcode.IsFixedMap(code) || code == msgpcode.Map16 || code == msgpcode.Map32:
			n, err := dec.DecodeMapLen()
			if err != nil || n < 0 || n > 4096 || n > budget {
				return nil, invalid
			}
			out := Obj{}
			for i := 0; i < n; i++ {
				key, err := read(depth + 1)
				name, ok := key.(string)
				if err != nil || !ok {
					return nil, invalid
				}
				if _, exists := out[name]; exists {
					return nil, invalid
				}
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				out[name] = value
			}
			return out, nil
		case msgpcode.IsFixedArray(code) || code == msgpcode.Array16 || code == msgpcode.Array32:
			n, err := dec.DecodeArrayLen()
			if err != nil || n < 0 || n > 4096 || n > budget {
				return nil, invalid
			}
			out := make([]any, 0, n)
			for i := 0; i < n; i++ {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			return out, nil
		case msgpcode.IsString(code):
			text, err := dec.DecodeString()
			if err != nil || !utf8.ValidString(text) {
				return nil, invalid
			}
			return text, nil
		case code == msgpcode.Nil:
			return nil, dec.DecodeNil()
		case code == msgpcode.True || code == msgpcode.False:
			return dec.DecodeBool()
		case code == msgpcode.Float || code == msgpcode.Double:
			n, err := dec.DecodeFloat64()
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, invalid
			}
			return n, nil
		case code >= msgpcode.Uint8 && code <= msgpcode.Uint64:
			n, err := dec.DecodeUint64()
			return float64(n), err
		case msgpcode.IsFixedNum(code) || code >= msgpcode.Int8 && code <= msgpcode.Int64:
			n, err := dec.DecodeInt64()
			return float64(n), err
		default:
			return nil, invalid
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, true, err
	}
	if _, err := dec.PeekCode(); err != io.EOF {
		return nil, true, invalid
	}
	return value.(Obj), true, nil
}
