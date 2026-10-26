"""Synthetic history for the isolated browser-test database only."""
import json
import pathlib
import sqlite3
import sys
import time

directory = pathlib.Path(sys.argv[1]).resolve()
if not directory.name.startswith("pm-history-browser-"):
    raise ValueError("History seeding is restricted to the isolated test directory")
now = int(time.time() // 60) * 60000
with sqlite3.connect(directory / "manager.sqlite") as database:
    for model, mac in [("TC1", "aabbcc000301"), ("M1", "aabbcc000302"), ("A1", "aabbcc000303")]:
        device_id = model.lower() + "-" + mac
        database.execute("DELETE FROM telemetry WHERE device_id=?", (device_id,))
        for minute in range(23 * 60):
            at = now - (23 * 60 - minute) * 60000
            # Leave a visible offline gap and invalid sensor period.
            if 750 <= minute <= 810:
                continue
            # Discrete readings and small sample variations keep all preview
            # curves representative of device telemetry.
            loads = [8, 8, 12, 38, 96, 42, 18, 64, 126, 52, 22, 12, 84, 148, 58, 16, 72, 32, 104, 46, 18, 66, 26, 12]
            power = loads[minute // 60] + [0, 2, -1, 4, -2, 1][(minute // 5) % 6]
            air = [11, 10, 9, 12, 18, 26, 21, 14, 11, 16, 20, 14, 12, 10, 17, 28, 23, 16, 12, 18, 24, 17, 14, 12][minute // 60]
            variation = [0, 1, -1, 2, 0, -1][(minute // 5) % 6]
            speed = [25, 25, 30, 40, 55, 70, 50, 35, 30, 45, 60, 40, 30, 25, 45, 65, 50, 40, 30, 50, 55, 40, 30, 35][minute // 60]
            values = {"power": power, "meter": 10 + minute * 0.001, "costTotal": minute * 0.0006} if model == "TC1" else {"pm25": air + variation, "hcho": 0.015 + air * 0.0003, "temp": 23 + air * 0.08, "humidity": 46 + air * 0.3, "co2": 520 + air * 5} if model == "M1" else {"speed": speed, "filter": 93 - minute * 0.0001}
            database.execute("INSERT INTO telemetry(device_id,minute,at,value) VALUES (?,?,?,?)", (device_id, at // 60000, at, json.dumps(values)))
print("Synthetic minute history with an offline gap seeded.")
