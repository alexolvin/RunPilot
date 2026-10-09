#!/usr/bin/env python3
"""Э4 e2e harness: 2×fakellm (по 1 слоту), 6×mockcoder × 3 хода.

Стенд: координатор со встроенным узлом (embedded_node), tmux-сокет `e2e`
(НЕ default). Отдельного процесса узла нет. Бинарники — в каталоге стенда.

Контрольные линии Э4 (ТЗ):
  - одновременных аренд на сервер ≤ 1 на каждом сэмпле (шаг 100 мс);
  - 0 таймаутов клиента; ровно 18 ходов OK, 0 фантомных;
  - dispatch p95 ≤ dispatch.budget_p95_ms (1500);
  - снятие аренды ≤ done_quiet+done_stable+2 с от последнего запроса.

Запуск: python3 test/e2e/e4_harness.py
  (бинарники предварительно собраны в STAND_DIR: runpilot, runpilot-fakellm,
   runpilot-mockcoder)
"""
import json
import os
import signal
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

STAND = os.environ.get("STAND_DIR", "/tmp/runpilot-stand")
COORD = "http://127.0.0.1:18788"
GW = "http://127.0.0.1:18787"
DB = f"{STAND}/runpilot.db"
SOCK = "e2e"
TOKEN = "stand-token-1234567890abcdef"
WORKDIR = "/mdisk/myhub/test"
MOCKCODER = f"{STAND}/runpilot-mockcoder"

NS = 6            # сессий
NTURNS = 3        # ходов на сессию
POSTS = 3         # POST на ход
SAMPLE_S = 0.1    # шаг сэмплирования аренд (100 мс)
DISPATCH_BUDGET_P95 = 1500
RELEASE_BUDGET_S = 4   # done_quiet(1)+done_stable(1)+2
ROUND_TIMEOUT = 120
TOTAL_TIMEOUT = 600

procs = []
LOG = open(f"{STAND}/harness.log", "w", buffering=1)


def log(msg):
    line = f"{time.strftime('%H:%M:%S')} {msg}"
    LOG.write(line + "\n")
    print(line, flush=True)


def db():
    return sqlite3.connect(DB, timeout=10)


def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        COORD + path, data=data, method=method,
        headers={"Content-Type": "application/json",
                 "Authorization": f"Bearer {TOKEN}"})
    with urllib.request.urlopen(req, timeout=15) as r:
        raw = r.read()
        return r.status, (json.loads(raw) if raw else {})


def start_proc(name, args, env=None):
    logf = open(f"{STAND}/{name}.log", "w")
    e = dict(os.environ)
    if env:
        e.update(env)
    p = subprocess.Popen(args, stdout=logf, stderr=subprocess.STDOUT, env=e,
                         cwd=STAND)
    procs.append((name, p, logf))
    return p


STAND_CONFIG = """\
coordinator:
  bind: 127.0.0.1
  allow_cidrs:
    - 127.0.0.1/32
  api_port: 18788
  gateway_port: 18787
  gateway_url: http://127.0.0.1:18787
  token_env: RUNPILOT_TOKEN
  db_path: {db}
  dist_dir: {dist}
  embedded_node: true
  heartbeat_sec: 1
  shutdown_grace_sec: 3
  bind_retry_sec: 1
gateway:
  hold_max_sec: 30
web:
  bind: 127.0.0.1
  port: 18789
node:
  host: e2e-node
  scan_interval_sec: 1
  tmux_sockets: [e2e]
  project_roots_default: [{workdir}]
turn:
  done_quiet_sec: 1
  done_stable_sec: 1
  auto_enqueue_stable_sec: 2
scheduler:
  snapshot_max_age_sec: 15
client:
  coordinator: http://127.0.0.1:18788
  token_env: RUNPILOT_TOKEN
monitor:
  health_interval_sec: 1
  metrics_interval_sec: 1
servers:
  - name: fakellm
    priority: 10
    slots: 1
    accept: [normal]
    health_url: http://127.0.0.1:18080/health
    metrics_url: http://127.0.0.1:18080/metrics
    upstreams:
      openai:
        url: http://127.0.0.1:18080
        model: srv-model
        key_env: FAKELLM_KEY
  - name: fakellm2
    priority: 10
    slots: 1
    accept: [normal]
    health_url: http://127.0.0.1:18081/health
    metrics_url: http://127.0.0.1:18081/metrics
    upstreams:
      openai:
        url: http://127.0.0.1:18081
        model: srv-model
        key_env: FAKELLM_KEY
profiles:
  qwen:
    model_alias: srv-model
    client_timeout_sec: 60
"""

STAND_PROFILE = """\
name: qwen
command: {mockcoder}
version_cmd: echo 0.24.4
submit_keys: Enter
cancel_keys: Escape
resume_text: continue
cmdline_regex: runpilot-mockcoder
classify_lines: 20
tab_width: 8
busy_regex: 'Enter to steer|esc to cancel'
approval_regex: 'Waiting for user confirmation'
wait_regex: 'Initializing|Retrying in [0-9]+s'
idle_regex: '^>'
placeholder_regex: '^Type your message'
input_top_regex: '^>'
input_bottom_regex: '^─+$'
input_strip_regex: '^>\\s*|\\s+\\x{{200B}}$|\\x{{200B}}'
volatile_regex: ''
env:
  OPENAI_BASE_URL: "{{{{.Gateway}}}}/s/{{{{.SID}}}}/v1"
  OPENAI_API_KEY: "runpilot"
  OPENAI_MODEL: "{{{{.ModelAlias}}}}"
  RUNPILOT_SID: "{{{{.SID}}}}"
"""


def write_stand_files():
    os.makedirs(f"{STAND}/home/.config/runpilot/profiles", exist_ok=True)
    os.makedirs(f"{STAND}/dist", exist_ok=True)
    with open(f"{STAND}/config.yaml", "w") as f:
        f.write(STAND_CONFIG.format(db=DB, dist=f"{STAND}/dist",
                                    workdir=WORKDIR))
    with open(f"{STAND}/home/.config/runpilot/profiles/qwen.yaml", "w") as f:
        f.write(STAND_PROFILE.format(mockcoder=MOCKCODER))


def cleanup():
    for name, p, logf in procs:
        try:
            p.send_signal(signal.SIGTERM)
        except Exception:
            pass
    for name, p, logf in procs:
        try:
            p.wait(timeout=5)
        except Exception:
            try:
                p.kill()
            except Exception:
                pass
        logf.close()
    subprocess.run(["tmux", "-L", SOCK, "kill-server"], capture_output=True)


def wait_api(timeout=30):
    t0 = time.time()
    while time.time() - t0 < timeout:
        try:
            st, _ = api("GET", "/api/v1/state")
            if st == 200:
                return True
        except Exception:
            pass
        time.sleep(0.3)
    return False


def create_session(name):
    try:
        st, out = api("POST", "/api/v1/run", {
            "name": name, "host": "e2e-node", "host_ip": "127.0.0.1",
            "profile": "qwen", "test": True, "dir": WORKDIR,
            "prio": "normal", "auto_enqueue": True})
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        log(f"run {name}: HTTP {e.code} {body}")
        raise
    assert st == 201, f"run {name}: {st} {out}"
    return out["sid"]


def start_mockcoder(name, sid, turns, posts):
    env = {
        "OPENAI_BASE_URL": f"{GW}/s/{sid}/v1",
        "OPENAI_API_KEY": "runpilot",
        "OPENAI_MODEL": "srv-model",
        "RUNPILOT_SID": sid,
    }
    subprocess.run(["tmux", "-L", SOCK, "kill-session", "-t", name],
                   capture_output=True)
    args = ["tmux", "-L", SOCK, "new-session", "-d", "-s", name, "-c", WORKDIR]
    for k, v in env.items():
        args += ["-e", f"{k}={v}"]
    args += [MOCKCODER, "--turns", str(turns), "--posts", str(posts)]
    subprocess.run(args, check=True)
    pane = subprocess.run(
        ["tmux", "-L", SOCK, "list-panes", "-t", name + ":0", "-F", "#{pane_id}"],
        check=True, capture_output=True, text=True).stdout.strip()
    subprocess.run(["tmux", "-L", SOCK, "set-option", "-p", "-t", pane,
                    "@runpilot_sid", sid], check=True)
    api("PATCH", f"/api/v1/sessions/{sid}/pane",
        {"tmux_session": name, "pane_id": pane, "agent_version": ""})
    return pane


def wait_pane(sid, timeout=20):
    t0 = time.time()
    while time.time() - t0 < timeout:
        _, st = api("GET", "/api/v1/state")
        for p in st.get("panes", []):
            if p.get("Pane", {}).get("sid") == sid:
                return p
        time.sleep(0.3)
    raise TimeoutError(f"панель {sid} не появилась за {timeout} с")


def send_text(name, text):
    """Ввести текст в панель (Enter НЕ нажимать — его жмёт диспетчер)."""
    subprocess.run(["tmux", "-L", SOCK, "send-keys", "-t", name, text],
                   check=True)


def main():
    t_start = time.time()
    log(f"Э4 harness: {NS} sessions × {NTURNS} turns × {POSTS} posts")
    log(f"COORD={COORD} GW={GW} DB={DB} SOCK={SOCK}")

    # Чистый DB + конфиг стенда
    for f in (DB, DB + "-shm", DB + "-wal"):
        if os.path.exists(f):
            os.remove(f)
    write_stand_files()

    try:
        # --- 1. fakellm ×2 + координатор (встроенный узел) ---
        start_proc("fakellm1", [f"{STAND}/runpilot-fakellm", "-port", "18080"])
        start_proc("fakellm2", [f"{STAND}/runpilot-fakellm", "-port", "18081"])
        time.sleep(0.5)
        start_proc("coord", [f"{STAND}/runpilot", "serve",
                             "--config", f"{STAND}/config.yaml"],
                   env={"RUNPILOT_TOKEN": TOKEN,
                        "HOME": f"{STAND}/home",
                        "FAKELLM_KEY": "stand-fakellm-key"})
        if not wait_api():
            log("FAIL: координатор не ответил")
            sys.exit(1)
        log("координатор готов (embedded node)")
        # Дебаг: состояние серверов
        time.sleep(2)
        try:
            _, servers = api("GET", "/api/v1/servers")
            for s in servers:
                log(f"  сервер {s.get('name')}: state={s.get('state')} "
                    f"slots={s.get('slots')}")
        except Exception as e:
            log(f"  servers: {e}")

        # --- 2. 6 сессий + mockcoder ---
        run_tag = int(time.time())
        sessions = []
        for i in range(NS):
            name = f"e4-{run_tag}-s{i+1}"
            sid = create_session(name)
            pane = start_mockcoder(name, sid, NTURNS, POSTS)
            wait_pane(sid)
            sessions.append({"name": name, "sid": sid, "pane": pane})
            log(f"  {name} sid={sid} pane={pane}")
        time.sleep(2)  # стабилизация IDLE
        log(f"создано {NS} сессий")

        # --- 3. сэмплер аренд (100 мс) ---
        lease_samples = []
        lease_violations = []
        stop = threading.Event()

        def sampler():
            while not stop.is_set():
                try:
                    c = db()
                    rows = c.execute(
                        "SELECT server, COUNT(*) FROM lease "
                        "WHERE state IN ('PENDING','ACTIVE') "
                        "GROUP BY server").fetchall()
                    c.close()
                    mx = max((r[1] for r in rows), default=0)
                    lease_samples.append({"t": round(time.time() - t_start, 1),
                                          "leases": {r[0]: r[1] for r in rows},
                                          "max": mx})
                    if mx > 1:
                        lease_violations.append(
                            {"t": round(time.time() - t_start, 1),
                             "leases": {r[0]: r[1] for r in rows}})
                except Exception:
                    pass
                time.sleep(SAMPLE_S)

        sampler_thread = threading.Thread(target=sampler, daemon=True)
        sampler_thread.start()

        # --- 4. NTURNS раундов по NS промптов ---
        for rnd in range(1, NTURNS + 1):
            for s in sessions:
                send_text(s["name"], f"промпт {s['name']} ход {rnd}")
            log(f"раунд {rnd}: промпты в {NS} сессий")
            t0 = time.time()
            done = False
            while time.time() - t0 < ROUND_TIMEOUT:
                ok = True
                for s in sessions:
                    c = db()
                    try:
                        n = c.execute(
                            "SELECT COUNT(*) FROM turn WHERE sid=? "
                            "AND ended_at!=''", (s["sid"],)).fetchone()[0]
                        row = c.execute(
                            "SELECT state FROM session WHERE sid=?",
                            (s["sid"],)).fetchone()
                        stt = row[0] if row else "GONE"
                    finally:
                        c.close()
                    if n < rnd or stt != "IDLE":
                        ok = False
                        break
                if ok:
                    done = True
                    break
                time.sleep(0.5)
            if done:
                log(f"раунд {rnd}: {NS} IDLE, {rnd} ходов закрыто "
                    f"({time.time() - t0:.0f} с)")
            else:
                log(f"раунд {rnd}: TIMEOUT ({ROUND_TIMEOUT} с)")

        stop.set()
        time.sleep(0.3)

        # --- 5. проверки ---
        c = db()
        turns = c.execute(
            "SELECT sid, outcome, requests FROM turn WHERE ended_at!=''"
        ).fetchall()
        turns_ok = [t for t in turns if t[1] == "OK"]
        turns_err = [t for t in turns if t[1] != "OK"]
        total_req = c.execute("SELECT COUNT(*) FROM request").fetchone()[0]
        req_by_status = c.execute(
            "SELECT status, COUNT(*) FROM request GROUP BY status").fetchall()
        release_rows = c.execute("""
            SELECT t.id, t.sid,
                   (julianday(t.ended_at) - julianday(MAX(r.t_end))) * 86400
                   AS gap
            FROM turn t
            LEFT JOIN request r ON r.sid = t.sid
                AND r.t_start >= t.started_at AND r.t_end <= t.ended_at
            WHERE t.ended_at != ''
            GROUP BY t.id
        """).fetchall()
        c.close()
        gaps = [r[2] for r in release_rows if r[2] is not None]
        max_gap = max(gaps, default=0)
        release_over = [r for r in release_rows
                        if r[2] is not None and r[2] > RELEASE_BUDGET_S]

        req = urllib.request.Request(
            COORD + "/api/v1/internal/dispatch-latency",
            headers={"Authorization": f"Bearer {TOKEN}"})
        with urllib.request.urlopen(req, timeout=10) as r:
            dl = json.loads(r.read())

        lease_max = max((s["max"] for s in lease_samples), default=0)
        checks = {
            f"{NS * NTURNS} ходов OK": len(turns_ok) == NS * NTURNS,
            "0 фантомных ходов": len(turns) == NS * NTURNS,
            "0 ошибок ходов": len(turns_err) == 0,
            f"все запросы доставлены ({NS * NTURNS * POSTS})":
                total_req == NS * NTURNS * POSTS,
            "аренд ≤1/сервер (0 нарушений)": len(lease_violations) == 0,
            f"снятие аренды ≤{RELEASE_BUDGET_S}с": len(release_over) == 0,
            f"dispatch p95 ≤{DISPATCH_BUDGET_P95}мс":
                dl.get("p95_ms", 10**9) <= DISPATCH_BUDGET_P95,
        }
        result = {
            "ns": NS, "nturns": NTURNS, "posts": POSTS,
            "duration_sec": round(time.time() - t_start, 1),
            "turns_total": len(turns),
            "turns_ok": len(turns_ok),
            "turns_err": [{"sid": t[0], "outcome": t[1]} for t in turns_err],
            "requests_total": total_req,
            "requests_by_status": {str(s): n for s, n in req_by_status},
            "expected_requests": NS * NTURNS * POSTS,
            "lease_samples": len(lease_samples),
            "lease_max_seen": lease_max,
            "lease_violations": lease_violations,
            "lease_violation_count": len(lease_violations),
            "release_max_gap_sec": round(max_gap, 2),
            "release_budget_sec": RELEASE_BUDGET_S,
            "release_over_budget": release_over,
            "dispatch_p95_ms": dl.get("p95_ms"),
            "dispatch_samples_n": dl.get("n"),
            "dispatch_budget_p95_ms": DISPATCH_BUDGET_P95,
            "checks": checks,
            "PASS": all(checks.values()),
        }

        log("=== ИТОГ ===")
        for k, v in checks.items():
            log(f"  [{'PASS' if v else 'FAIL'}] {k}")
        log(f"  ходов OK: {len(turns_ok)}/{NS * NTURNS}; "
            f"запросов: {total_req}")
        log(f"  аренд: max={lease_max}, нарушений={len(lease_violations)}")
        log(f"  снятие аренды max gap: {max_gap:.2f} с "
            f"(бюджет {RELEASE_BUDGET_S})")
        log(f"  dispatch p95: {dl.get('p95_ms')} мс "
            f"(бюджет {DISPATCH_BUDGET_P95})")
        log(f"  ИТОГО: {'PASS' if result['PASS'] else 'FAIL'}")

        with open(f"{STAND}/e4-result.json", "w") as f:
            json.dump(result, f, ensure_ascii=False, indent=2)
        sys.exit(0 if result["PASS"] else 1)

    finally:
        cleanup()


if __name__ == "__main__":
    main()
