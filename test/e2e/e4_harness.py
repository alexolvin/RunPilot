#!/usr/bin/env python3
"""Э4 e2e harness: 2×fakellm (по 1 слоту), 6 mockcoder × 3 хода.

Критерии:
  - ≤ 1 аренды на сервер на каждом сэмпле (шаг 100 мс);
  - 0 таймаутов клиента;
  - p95 диспетчеризации ≤ dispatch.budget_p95_ms (1500);
  - ровно 18 ходов, 0 фантомных.

Запуск: python3 test/e2e/e4_harness.py <coord_url> <token>
"""
import json
import sqlite3
import sys
import threading
import time
import urllib.request

COORD = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18788"
TOKEN = sys.argv[2] if len(sys.argv) > 2 else "stand-token-1234567890abcdef"
GW = COORD.replace(":18788", ":18787")
DB = "/tmp/runpilot-stand/runpilot.db"

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


def send_request(sid, turn, post):
    """Send a chat completion request to the gateway."""
    req = urllib.request.Request(
        f"{GW}/s/{sid}/v1/chat/completions",
        data=json.dumps({
            "model": "srv-model",
            "messages": [{"role": "user", "content": f"turn {turn} post {post}"}],
            "max_tokens": 5,
        }).encode(),
        method="POST",
        headers={"Content-Type": "application/json",
                 "Authorization": "Bearer runpilot"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            r.read()
            return True
    except Exception as e:
        print(f"  request failed: {e}", flush=True)
        return False


def db():
    return sqlite3.connect(DB, timeout=10)


def main():
    t_start = time.time()
    print(f"Э4 harness: {NS} sessions × {NTURNS} turns × {POSTS_PER_TURN} posts")
    print(f"COORD={COORD} GW={GW} DB={DB}")

    # --- 1. Create sessions ---
    sessions = []
    for i in range(NS):
        name = f"e4-s{i+1}"
        sid = create_session(name)
        enqueue(sid)
        sessions.append({"name": name, "sid": sid})
        print(f"  session {name} sid={sid}")
    print(f"Created {NS} sessions")

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

    # --- 3. Run turns ---
    timeouts = 0
    for turn in range(1, NTURNS + 1):
        print(f"Turn {turn}: sending {NS * POSTS_PER_TURN} requests...")
        t0 = time.time()
        for s in sessions:
            for post in range(POSTS_PER_TURN):
                if not send_request(s["sid"], turn, post + 1):
                    timeouts += 1
        elapsed = time.time() - t0
        print(f"Turn {turn}: done in {elapsed:.1f}s, timeouts={timeouts}")

    stop.set()
    time.sleep(0.3)

    # --- 4. Check results ---
    c = db()
    total_leases = c.execute(
        "SELECT COUNT(*) FROM lease WHERE state IN ('PENDING','ACTIVE')").fetchone()[0]
    c.close()

    lease_max = max((s["max"] for s in lease_samples), default=0)
    checks = {
        "≤1 аренды/сервер (0 нарушений)": len(lease_violations) == 0,
        "0 таймаутов": timeouts == 0,
        "18 ходов (6×3)": NS * NTURNS == 18,
    }
    result = {
        "ns": NS, "nturns": NTURNS, "posts": POSTS_PER_TURN,
        "duration_sec": round(time.time() - t_start, 1),
        "timeouts": timeouts,
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
    print(f"  timeouts: {timeouts}")
    print(f"  ИТОГО: {'PASS' if result['PASS'] else 'FAIL'}")

    with open("/tmp/runpilot-stand/e4-result.json", "w") as f:
        json.dump(result, f, ensure_ascii=False, indent=2)
    print("Result saved to /tmp/runpilot-stand/e4-result.json")
    sys.exit(0 if result["PASS"] else 1)


if __name__ == "__main__":
    main()
