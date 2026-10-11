# Changelog

Формат: [Keep a Changelog](https://keepachangelog.com/ru/1.1.0/).
Версии: semver (vMAJOR.MINOR.PATCH), источник — git-тег.

## [Unreleased]

### Добавлено
- W15: медиа через RunPilot — шлюз пропускает image_url/video_url, модальности
  серверов (input_modalities), окно контекста (max_model_len).
- W15: объявление модальностей сессиям Qwen Code (system-settings.json),
  перезапуск кодера (restart-agent), doctor qwen_system_settings.
- W15: учёт версий — git-тег vMAJOR.MINOR.PATCH (единственный источник),
  `make release BUMP=patch|minor|major`.

## [v0.1.0] — 2026-10-11

Первый релиз. RunPilot: очередь Qwen Code-кодеров в tmux по слотам локальных
AI-серверов.
