package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var exportZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// Protect spreadsheet text cells without changing numeric readings (including
// negative temperatures). Device names and rooms are user-controlled text.
func csvText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.ContainsAny(value, "\t\r\n") || strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") {
		return "'" + value
	}
	return value
}

func (s *Store) writeDataCSV(ctx context.Context, out io.Writer) error {
	rows, err := s.db.QueryContext(ctx, `SELECT t.device_id,t.at,t.value,
 COALESCE(d.model,''),COALESCE(d.mac,''),
 COALESCE(json_extract(d.value,'$.name'),''),COALESCE(json_extract(d.value,'$.room'),'')
 FROM telemetry t LEFT JOIN devices d ON d.id=t.device_id ORDER BY t.device_id,t.minute`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if _, err = io.WriteString(out, "\ufeff"); err != nil {
		return err
	}
	w := csv.NewWriter(out)
	w.UseCRLF = true
	if err = w.Write([]string{"设备ID", "设备名称", "型号", "MAC", "空间", "记录时间（北京时间）", "功率（W）", "累计用电（度）", "累计预估花费（元）", "PM2.5（μg/m³）", "甲醛（mg/m³）", "温度（°C）", "湿度（%）", "CO₂（ppm）", "风机输出（%）", "滤芯剩余（%）", "PM10（μg/m³）", "TVOC（ppb）"}); err != nil {
		return err
	}
	for rows.Next() {
		var id, raw, model, mac, name, room string
		var at int64
		if err = rows.Scan(&id, &at, &raw, &model, &mac, &name, &room); err != nil {
			return err
		}
		var values Obj
		if err = json.Unmarshal([]byte(raw), &values); err != nil {
			return err
		}
		// Removing a device keeps its saved readings. Include those rows too.
		if model == "" {
			parts := strings.SplitN(id, "-", 2)
			model = strings.ToUpper(parts[0])
			if len(parts) == 2 {
				mac = parts[1]
			}
		}
		record := []string{csvText(id), csvText(name), csvText(model), csvText(mac), csvText(room), time.UnixMilli(at).In(exportZone).Format("2006-01-02 15:04:05.000")}
		for _, key := range []string{"power", "meter", "costTotal", "pm25", "hcho", "temp", "humidity", "co2", "speed", "filter", "pm10", "tvoc"} {
			value := ""
			if n, ok := finite(values[key]).(float64); ok {
				value = strconv.FormatFloat(n, 'f', -1, 64)
			}
			record = append(record, value)
		}
		if err = w.Write(record); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func (a *App) exportData(w http.ResponseWriter, r *http.Request) (int, any, error) {
	// Prepare on disk so a slow download never holds the single SQLite
	// connection or builds the complete history in browser/server memory.
	file, err := os.CreateTemp("", "phicomm-history-*.csv")
	if err != nil {
		return 0, nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = a.manager.store.writeDataCSV(r.Context(), file); err != nil {
		return 0, nil, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, nil, err
	}
	filename := "phicomm-history-" + time.Now().In(exportZone).Format("20060102-150405") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filename, time.Time{}, file)
	return 0, nil, nil
}
