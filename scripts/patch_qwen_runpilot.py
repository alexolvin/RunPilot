#!/usr/bin/env python3
"""patch_qwen_runpilot.py — идемпотентный патч активного qwen-бандла.

Заменяет лейблы "(Runtime)"/"Runtime model" на RunPilot в status-баре (always
visible) и в диалоге /model АКТИВНОЙ версии qwen (авто-обновление, managed npm).
Находит активную версию по ~/.qwen/updates/npm/*/active.json, патчит два
UI-чанка (start-opentui-ui-*.js + startInteractiveUI-*.js), если они ещё не
патчены (маркер "RunPilot" в файле). Идемпотентно: повторный запуск ничего не
меняет.

Авто-обновление qwen НЕ отключается: после обновления (новая версия) скрипт
прогоняют заново — оператор или хук при старте узла.

Запуск: python3 scripts/patch_qwen_runpilot.py
"""
import glob
import json
import os
import sys


def main() -> int:
    home = os.path.expanduser("~")
    upd = os.environ.get("QWEN_UPDATES", os.path.join(home, ".qwen", "updates", "npm"))
    actives = sorted(glob.glob(os.path.join(upd, "*", "active.json")))
    if not actives:
        print("active.json не найден — авто-обновление qwen не использовалось")
        return 0
    with open(actives[0], encoding="utf-8") as f:
        version = json.load(f)["version"]
    pkg = os.path.join(
        os.path.dirname(actives[0]), "versions", version,
        "node_modules", "@qwen-code", "qwen-code",
    )
    chunks = []
    for pat in ("start-opentui-ui-*.js", "startInteractiveUI-*.js"):
        chunks += glob.glob(os.path.join(pkg, "chunks", pat))
    if not chunks:
        print("UI-чанки не найдены:", pkg)
        return 1

    # Точные замены (старое -> новое). .replace идемпотентен по маркеру "RunPilot":
    # если бандл уже патчен, «старых» подстрок нет — ничего не меняется.
    pairs = [
        # status-бар (opentui): "API Key | <model>" -> "... (RunPilot)"
        ('authModelText=authLabel?`${authLabel} | ${model}`:model;',
         'authModelText=authLabel?`${authLabel} | ${model} (RunPilot)`:`${model} (RunPilot)`;'),
        # status-бар (interactive): "<type> | <model>" -> "... (RunPilot)"
        ('authModelText=`${formattedAuthType} | ${model}`;',
         'authModelText=`${formattedAuthType} | ${model} (RunPilot)`;'),
        # /model: inline-суффикс имени модели
        ('" (Runtime)"', '" (RunPilot)"'),
        # /model: description
        ('`${description} (Runtime)`:"Runtime model"',
         '`${description} (RunPilot)`:"RunPilot model"'),
    ]

    print("активная версия qwen:", version)
    for path in chunks:
        with open(path, encoding="utf-8") as f:
            src = f.read()
        if "RunPilot" in src:
            print("  уже патчен:", os.path.basename(path))
            continue
        n = 0
        for old, new in pairs:
            if old in src:
                src = src.replace(old, new)
                n += 1
        if n == 0:
            print("  паттерны не найдены (формат изменился?):", os.path.basename(path))
            continue
        with open(path, "w", encoding="utf-8") as f:
            f.write(src)
        print(f"  патчен ({n}): {os.path.basename(path)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
