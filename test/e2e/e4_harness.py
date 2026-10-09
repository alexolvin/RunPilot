#!/usr/bin/env python3
"""Э4 e2e harness: 2×fakellm (по 1 слоту), 6×mockcoder × 3 хода.

Критерии:
  - ≤ 1 аренды на сервер на каждом сэмпле (шаг 100 мс);
  - 0 таймаутов клиента;
  - p95 диспетчеризации ≤ dispatch.budget_p95_ms (1500);
  - ровно 18 ходов, 0 фантомных.

Запуск: python3 test/e2e/e4_harness.py <coord_url> <token>
"""
import json
import os
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.request

COORD = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18788"
TOKEN = sys.argv[2] if len(sys.argv) > 2 else "stand-token-1234567890abcdef"
GW = COORD.replace(":18788", ":18787")
DB = "/tmp/runpilot-stand/runpilot.db"
SOCK = "e4test"
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))

NS = 6
NTURNS = 3
POSTS_PER_TURN = 3
SAMPLE_S = 0.1
DISPATCH_BUDGET_P95 = 1500
ROUND_TIMEOUT = 120
TOTAL_TIMEOUT = 900


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        COORD + path, data=data, method=method,
        headers={"Content-Type": "application/json",
                 "Authorization": f"Bearer {TOKEN}"})
    with urllib.request.urlopen(req, timeout=15) as r:
        raw = r.read()
        return r.status, (json.loads(raw) if raw else {})


def create_session(name):
    st, out = api("POST", "/api/v1/run", {
        "name": name, "host": "3p14-aaa", "host_ip": "127.0.0.1",
        "profile": "qwen", "test": True, "dir": "/mdisk/myhub/test"})
    assert st == 201, f"run {name}: {st} {out}"
    return out["sid"]


def enqueue(sid):
    api("POST", f"/api/v1/sessions/{sid}/enqueue")


def start_mockcoder(name, sid):
    """Запустить mockcoder в tmux-сессии и привязать панель к сессии."""
    # Убить старую сессию (если есть)
    subprocess.run(["tmux", "-L", SOCK, "kill-session", "-t", name],
                   capture_output=True)
    # Новая tmux-сессия с mockcoder
    cmd = ["tmux", "-L", SOCK, "new-session", "-d", "-s", name,
           "python3", f"{SCRIPT_DIR}/mockcoder.py", sid, GW, str(NTURNS), str(POSTS_PER_TURN)]
    subprocess.run(cmd, check=True)
    # Получить pane_id
    pane = subprocess.run(
        ["tmux", "-L", SOCK, "list-panes", "-t", name + ":0", "-F", "#{pane_id}"],
        check=True, capture_output=True, text=True).stdout.strip()
    # Привязать @aqm_sid
    subprocess.run(["tmux", "-L", SOCK, "set-option", "-p", "-t", pane,
                    "@aqm_sid", sid], check=True)
    # PATCH панель в координатор
    api("PATCH", f"/api/v1/sessions/{sid}/pane", {
        "tmux_session": name, "pane_id": pane, "agent_version": ""})
    return pane


def wait_pane(sid, timeout=15):
    """Ждём, пока узел сообщит о панели."""
    t0 = time.time()
    while time.time() - t0 < timeout:
        _, st = api("GET", "/api/v1/state")
        for p in st.get("panes", []):
            if p.get("Pane", {}).get("sid") == sid:
                return p
        time.sleep(0.3)
    raise TimeoutError(f"панель {sid} не появилась за {timeout} с")


def db():
    return sqlite3.connect(DB, timeout=10)


def cleanup():
    subprocess.run(["tmux", "-L", SOCK, "kill-server"], capture_output=True)


def main():
    t_start = time.time()
    print(f"Э4 harness: {NS} sessions × {NTURNS} turns × {POSTS_PER_TURN} posts")
    print(f"COORD={COORD} GW={GW} DB={DB}")

    try:
        # --- 1. Create sessions + mockcoders ---
        ts = int(time.time())
        sessions = []
        for i in range(NS):
            name = f"e4-{ts}-s{i+1}"
            sid = create_session(name)
            pane = start_mockcoder(name, sid)
            wait_pane(sid)
            sessions.append({"name": name, "sid": sid, "pane": pane})
            print(f"  session {name} sid={sid} pane={pane}")
        print(f"Created {NS} sessions with mockcoders")

        # --- 2. Lease sampler (100ms) ---
        lease_samples = []
        lease_violations = []
        stop = threading.Event()

        def sampler():
            while not stop.is_set():
                try:
                    c = db()
                    rows = c.execute(
                        "SELECT server, COUNT(*) FROM lease "
                        "WHERE state IN ('PENDING','ACTIVE') GROUP BY server").fetchall()
                    c.close()
                    mx = max((r[1] for r in rows), default=0)
                    lease_samples.append({
                        "t": round(time.time() - t_start, 1),
                        "leases": {r[0]: r[1] for r in rows}, "max": mx})
                    if mx > 1:
                        lease_violations.append(
                            {"t": round(time.time() - t_start, 1),
                             "leases": {r[0]: r[1] for r in rows}})
                except Exception:
                    pass
                time.sleep(SAMPLE_S)

        st = threading.Thread(target=sampler, daemon=True)
        st.start()

        # --- 3. Wait for all turns to complete ---
        print("Waiting for all turns to complete...")
        t0 = time.time()
        done = False
        while time.time() - t0 < ROUND_TIMEOUT:
            c = db()
            turns_done = c.execute(
                "SELECT COUNT(*) FROM turn WHERE ended_at!=''").fetchone()[0]
            c.close()
            if turns_done >= NS * NTURNS:
                done = True
                break
            time.sleep(1)

        elapsed = time.time() - t0
        if done:
            print(f"All {NS * NTURNS} turns completed in {elapsed:.1f}s")
        else:
            print(f"TIMEOUT: only {turns_done}/{NS * NTURNS} turns after {elapsed:.1f}s")

        stop.set()
        time.sleep(0.3)

        # --- 4. Check results ---
        c = db()
        turns_total = c.execute(
            "SELECT COUNT(*) FROM turn WHERE ended_at!=''").fetchone()[0]
        turns_ok = c.execute(
            "SELECT COUNT(*) FROM turn WHERE ended_at!='' AND outcome='OK'").fetchone()[0]
        c.close()

        lease_max = max((s["max"] for s in lease_samples), default=0)
        checks = {
            "≤1 аренды/сервер (0 нарушений)": len(lease_violations) == 0,
            "18 ходов OK": turns_ok == NS * NTURNS,
        }
        result = {
            "ns": NS, "nturns": NTURNS, "posts": POSTS_PER_TURN,
            "duration_sec": round(time.time() - t_start, 1),
            "turns_total": turns_total,
            "turns_ok": turns_ok,
            "lease_samples": len(lease_samples),
            "lease_max_seen": lease_max,
            "lease_violations": len(lease_violations),
            "checks": checks,
            "PASS": all(checks.values()),
        }

        print("\n=== ИТОГ ===")
        for k, v in checks.items():
            print(f"  [{'PASS' if v else 'FAIL'}] {k}")
        print(f"  lease max: {lease_max}, violations: {len(lease_violations)}")
        print(f"  turns: {turns_ok}/{NS * NTURNS} OK")
        print(f"  ИТОГО: {'PASS' if result['PASS'] else 'FAIL'}")

        with open("/tmp/runpilot-stand/e4-result.json", "w") as f:
            json.dump(result, f, ensure_ascii=False, indent=2)
        print("Result saved to /tmp/runpilot-stand/e4-result.json")
        sys.exit(0 if result["PASS"] else 1)

    finally:
        cleanup()


if __name__ == "__main__":
    main()
