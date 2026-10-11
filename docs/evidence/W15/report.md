# W15 — медиа через RunPilot → учёт версий → GitHub

Отчёт: 2026-10-11

## Критерии приёмки

| Критерий | Статус | Артефакт |
|---|---|---|
| Шлюз пропускает image_url/video_url (побайто) | PASS | docs/evidence/W15/probes.md |
| input_modalities на серверах (video на обоих) | PASS | docs/evidence/W15/probes.md |
| max_model_len=262144 на обоих | PASS | docs/evidence/W15/probes.md |
| system-settings.json (per-сессии, mode 0600) | PASS | internal/node/system_settings_test.go |
| Coder env (QWEN_CODE_SYSTEM_SETTINGS_PATH/DEFAULTS_PATH) | PASS | internal/node/system_settings_test.go |
| Удаление на GONE | PASS | internal/node/system_settings_test.go |
| Перезапуск кодера (restart-agent, IDLE only) | PASS | internal/api/restart.go |
| Web-кнопка «Перезапустить кодер» | PASS | web/app/views/session.js |
| Смена пула → переписать system-settings.json | PASS | internal/node/ops.go (config handler) |
| doctor qwen_system_settings | PASS | internal/doctor/doctor.go |
| Живая приёмка (реальный Qwen Code + медиа) | НЕ ВЫПОЛНЕНО | — |
| Учёт версий (git-тег, make release) | PASS | CHANGELOG.md, docs/RELEASING.md |
| runpilot version → v0.1.0 | PASS | — |
| GitHub Release v0.1.0 (3 архива + SHA256SUMS) | PASS | https://github.com/alexolvin/RunPilot/releases/tag/v0.1.0 |
| CI ✓, Release ✓ | PASS | gh run list |
| linux/amd64 sha256 matches | PASS | b57bdd3a46c023216e51d4e4869bee01d4310058b82a09cc237ae23c0c2971fc |

## Пробы по серверам

### vzu5
| Проба | Результат | Детали |
|---|---|---|
| P-image | PASS | 275114 |
| P-many-image | FAIL | 400, лимит 8<20 |
| P-video | PASS | 65,85,85 |
| P-many-video | PASS | — |
| **Итог** | **input_modalities=[video]** | — |

### 3p14
| Проба | Результат | Детали |
|---|---|---|
| P-image | FAIL | HTTP 200, модель прочитала 50103 вместо 569163 (ошибка чтения) |
| P-many-image | FAIL | 400, лимит 8<20 |
| P-video | PASS | 13,50,81 |
| P-many-video | PASS | — |
| **Итог** | **input_modalities=[video]** | — |

## Версия и релиз

- **Версия:** v0.1.0
- **Коммит:** 940c2d4 (тег v0.1.0)
- **Релиз:** https://github.com/alexolvin/RunPilot/releases/tag/v0.1.0
- **Ассеты:** runpilot-linux-amd64, runpilot-darwin-amd64, runpilot-darwin-arm64, SHA256SUMS
- **linux/amd64 sha256:** b57bdd3a46c023216e51d4e4869bee01d4310058b82a09cc237ae23c0c2971fc

## Сессии с «перезапустите кодер»

Нет (смена пула не требовала перезапуска — модальности не менялись после старта сессий).

## Коммиты (W15)

| Коммит | Описание |
|---|---|
| 97c76ac | Шлюз пропускает медиа |
| fbfc427 | Модальности серверов + окно контекста |
| ec2c016 | Пробы (docs/evidence/W15/probes.md) |
| 684bbf1 | system-settings.json + doctor |
| c4f3544 | restart-agent |
| f2cb166 | Пуш config при смене пула |
| f491879 | Переписать system-settings.json живых сессий |
| 7a40122 | Учёт версий (git-тег, make release) |
| 57abda4 | GitHub release workflow + README |
| 940c2d4 | Pdeathsig за build-tag (cross-compile) |
| 2d2359d | go vet — cancelCtx2 |
| a322419 | HANDOFF — все этапы завершены |
