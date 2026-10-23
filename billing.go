package main

import (
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

func defaultTariff() Obj {
	return Obj{"mode": "disabled", "flatRate": nil, "peakRate": nil, "valleyRate": nil, "valleyStart": "22:00", "valleyEnd": "08:00", "revision": "disabled"}
}

var ratePattern = regexp.MustCompile(`^\d+(?:\.\d{1,4})?$`)
var clockPattern = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)

func validateTariff(input, prev Obj) (Obj, error) {
	mode := str(input["mode"])
	if !contains([]string{"disabled", "flat", "tou"}, mode) {
		return nil, bad("请选择有效的计费方式")
	}
	next := clone(prev)
	next["mode"] = mode
	for _, key := range []string{"flatRate", "peakRate", "valleyRate"} {
		raw, present := input[key]
		if !present {
			raw = prev[key]
		}
		required := mode == "flat" && key == "flatRate" || mode == "tou" && key != "flatRate"
		if raw == nil || raw == "" {
			if required {
				return nil, bad("请填写电价")
			}
			next[key] = nil
			continue
		}
		var value string
		switch v := raw.(type) {
		case string:
			value = v
		case float64:
			value = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			return nil, bad("电价格式不正确")
		}
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil || !ratePattern.MatchString(value) || rate > 1000 {
			return nil, bad("电价须为 0–1000 元/度，最多 4 位小数")
		}
		next[key] = rate
	}
	for _, key := range []string{"valleyStart", "valleyEnd"} {
		if raw, present := input[key]; present {
			next[key] = raw
		}
		if !clockPattern.MatchString(str(next[key])) {
			return nil, bad("谷时时间须为有效的小时和分钟")
		}
	}
	if mode == "tou" && next["valleyStart"] == next["valleyEnd"] {
		return nil, bad("谷时开始与结束时间不能相同")
	}
	if !reflect.DeepEqual(next, prev) {
		next["revision"] = token()
	}
	return next, nil
}

const dayMS = 86400000.0

func minuteOf(value any) float64 {
	s := strings.Split(str(value), ":")
	if len(s) != 2 {
		return 0
	}
	h, _ := strconv.Atoi(s[0])
	m, _ := strconv.Atoi(s[1])
	return float64(h*60+m) * 60000
}
func valleyElapsed(at float64, c Obj) float64 {
	local := at + 8*3600000
	days := math.Floor(local / dayMS)
	within := local - days*dayMS
	start, end := minuteOf(c["valleyStart"]), minuteOf(c["valleyEnd"])
	overlap := func(a, b float64) float64 { return math.Max(0, math.Min(within, b)-a) }
	if start < end {
		return days*(end-start) + overlap(start, end)
	}
	return days*(dayMS-start+end) + overlap(0, end) + overlap(start, dayMS)
}
func currentPrice(c Obj, at float64) (any, any) {
	if c["mode"] == "disabled" {
		return nil, nil
	}
	if c["mode"] == "flat" {
		return c["flatRate"], "flat"
	}
	within := math.Mod(at+8*3600000, dayMS)
	start, end := minuteOf(c["valleyStart"]), minuteOf(c["valleyEnd"])
	valley := start < end && within >= start && within < end || start >= end && (within >= start || within < end)
	if valley {
		return c["valleyRate"], "valley"
	}
	return c["peakRate"], "peak"
}
func recordEnergy(previous Obj, energy any, c Obj, at float64) Obj {
	n, valid := finite(energy).(float64)
	if c["mode"] == "disabled" || !valid || n < 0 {
		return previous
	}
	bill := previous
	if bill == nil {
		bill = Obj{"since": at, "energy": float64(0), "cost": float64(0), "flatEnergy": float64(0), "peakEnergy": float64(0), "valleyEnergy": float64(0), "resets": float64(0)}
	} else {
		bill = clone(previous)
	}
	if bill["revision"] != c["revision"] || bill["lastEnergy"] == nil {
		bill["revision"] = c["revision"]
		bill["lastEnergy"] = n
		bill["lastAt"] = at
		return bill
	}
	from := num(bill["lastAt"])
	if at <= from {
		return previous
	}
	delta := n - num(bill["lastEnergy"])
	bill["lastEnergy"] = n
	bill["lastAt"] = at
	if delta < 0 {
		bill["resets"] = num(bill["resets"]) + 1
		return bill
	}
	bill["energy"] = num(bill["energy"]) + delta
	if c["mode"] == "flat" {
		bill["flatEnergy"] = num(bill["flatEnergy"]) + delta
		bill["cost"] = num(bill["cost"]) + delta*num(c["flatRate"])
	} else {
		valley := delta * math.Min(1, math.Max(0, (valleyElapsed(at, c)-valleyElapsed(from, c))/(at-from)))
		bill["valleyEnergy"] = num(bill["valleyEnergy"]) + valley
		bill["peakEnergy"] = num(bill["peakEnergy"]) + delta - valley
		bill["cost"] = num(bill["cost"]) + valley*num(c["valleyRate"]) + (delta-valley)*num(c["peakRate"])
	}
	return bill
}
func billingSummary(b, c Obj, at float64) Obj {
	rate, period := currentPrice(c, at)
	out := Obj{"mode": c["mode"], "rate": rate, "period": period, "since": b["since"], "lastAt": b["lastAt"], "awaitingSample": c["mode"] != "disabled" && b["revision"] != c["revision"]}
	for _, k := range []string{"energy", "cost", "flatEnergy", "peakEnergy", "valleyEnergy", "resets"} {
		out[k] = num(b[k])
	}
	return out
}
