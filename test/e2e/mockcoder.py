#!/usr/bin/env python3
"""Mock coder: отправляет POST-запросы к шлюзу RunPilot.

Используется в e2e Э4: 6×mockcoder × 3 хода × 3 POST на ход.
Запуск в tmux: python3 mockcoder.py <sid> <gateway_url> <turns> <posts_per_turn>
"""
import json
import os
import sys
import time
import urllib.request

sid = sys.argv[1]
gw = sys.argv[2]
turns = int(sys.argv[3]) if len(sys.argv) > 3 else 3
posts = int(sys.argv[4]) if len(sys.argv) > 4 else 3

url = f"{gw}/s/{sid}/v1/chat/completions"
headers = {
    "Content-Type": "application/json",
    "Authorization": "Bearer runpilot",
}

for turn in range(1, turns + 1):
    for post in range(1, posts + 1):
        body = json.dumps({
            "model": "srv-model",
            "messages": [{"role": "user", "content": f"turn {turn} post {post}"}],
            "max_tokens": 5,
        }).encode()
        req = urllib.request.Request(url, data=body, method="POST", headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                r.read()
                print(f"turn={turn} post={post} ok", flush=True)
        except Exception as e:
            print(f"turn={turn} post={post} FAIL: {e}", flush=True)
    # Пауза между ходами: ждём, пока ход закроется (сессия → IDLE)
    time.sleep(2)

print("mockcoder done", flush=True)
