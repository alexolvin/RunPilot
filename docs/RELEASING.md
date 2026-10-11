# Релизы

## Источник версии

Единственный источник версии — **annotated git-тег** `vMAJOR.MINOR.PATCH`.
Бинарник читает версию из `debug.ReadBuildInfo().Main.Version` (Go VCS
stamping): при сборке из зачмеченного коммита — тег, иначе — `dev`.

ldflags, VERSION-файл и short-hash как источники версии **убраны** (W15).

## Создание релиза

```bash
make release BUMP=patch    # v0.1.0 → v0.1.1
make release BUMP=minor    # v0.1.0 → v0.2.0
make release BUMP=major    # v0.1.0 → v1.0.0
```

Целевой: `make release BUMP=patch|minor|major [MESSAGE="..."]`

Целевой:
1. Читает текущую версию из `git describe --tags --abbrev=0`.
2. Увеличивает версию (patch/minor/major).
3. Создаёт annotated git-тег.
4. Выводит новую версию.

После тега:
- `git push origin main --tags` — публикует тег.
- GitHub Actions (`.github/workflows/release.yml`) — собирает бинарники,
  создаёт GitHub Release с архивами + SHA256SUMS.

## Версия в бинарнике

```bash
runpilot version
# v0.1.0
# <full commit hash>
```

Версия видна в:
- `runpilot version` / `runpilot --version`
- hello-сообщение узла (WS)
- `GET /api/v1/state` (version)
- веб (sidebar, settings)
- doctor (node version check)

## CHANGELOG

`CHANGELOG.md` — обновляется перед каждым релизом (секция [Unreleased] →
[version]).
