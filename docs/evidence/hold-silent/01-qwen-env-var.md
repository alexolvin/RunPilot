# Этап 0 — п.1: версия qwen + наличие `QWEN_CODE_API_TIMEOUT_MS` в активном пакете

Дата: 2026-10-02. Машина: 3p14-aaa (хост окна), пользователь `user`.

## Команды по ТЗ (как даны) и их вывод

```
$ qwen --version
0.24.7

$ grep -rl QWEN_CODE_API_TIMEOUT_MS "$(npm root -g)/ @qwen-code/qwen-code"
grep: /home/user/.nvm/versions/node/v24.18.0/lib/node_modules/ @qwen-code/qwen-code: No such file or directory
```

`npm root -g` → `/home/user/.nvm/versions/node/v24.18.0/lib/node_modules`. Там пакета `@qwen-code/qwen-code` **нет** — qwen ставится managed npm auto-updater'ом, а не глобальным npm. В ТЗ в пути `$(npm root -g)/ @qwen-code/qwen-code` лишний пробел перед `@qwen-code` (опечатка) — путь в любом случае неверный для этой установки.

## Реальное расположение активного qwen

```
$ readlink -f "$(which qwen)"
/home/user/.local/lib/node_modules/@qwen-code/qwen-code/cli-entry.js

$ cat ~/.qwen/updates/npm/f8b0b7826ba50adc/active.json
{"version":"0.24.7","bootstrap":"/home/user/.local/lib/node_modules/@qwen-code/qwen-code/cli-entry.js","baseVersion":"0.23.3","bootstrapCtimeMs":1789064761406.6992}
```

Активная версия **0.24.7** (база-бутстрап 0.23.3). Реализуемый код 0.24.7:
`~/.qwen/updates/npm/f8b0b7826ba50adc/versions/0.24.7/node_modules/@qwen-code/qwen-code/`

## Исправленная команда (рабочий путь) и вывод

```
$ grep -rl QWEN_CODE_API_TIMEOUT_MS \
    ~/.qwen/updates/npm/*/versions/0.24.7/node_modules/@qwen-code/qwen-code/chunks/
~/.qwen/updates/npm/f8b0b7826ba50adc/versions/0.24.7/node_modules/@qwen-code/qwen-code/chunks/chunk-AN36BHDM.js
```

Строка присутствует в активном чанке 0.24.7 → **env-переменная поддерживается установленной версией**. (Также есть в бутстрапе `~/.local/lib/.../chunks/chunk-N7VWZDWW.js` и в `bundled/qc-helper/docs/configuration/settings.md`.)

## Логика применения (извлечено из chunk-AN36BHDM.js, 0.24.7)

```js
(env, generationConfig, sources, modelProvider) => {
  if (modelProvider?.generationConfig?.timeout !== void 0) return;   // modelProviders > env
  const raw = env["QWEN_CODE_API_TIMEOUT_MS"];
  if (raw === void 0) return;
  const trimmed = raw.trim();
  if (!/^\d+$/.test(trimmed)) return;
  const parsed = Number(trimmed);
  if (!Number.isSafeInteger(parsed)) return;
  generationConfig.timeout = parsed;
  sources["timeout"] = "env";
  ...
}
```

Подтверждено из исходников 0.24.7:
- **`QWEN_CODE_API_TIMEOUT_MS`** читается из env, валидируется (`/^\d+$/`, safeInteger), пишется в `generationConfig.timeout`.
- **Приоритет modelProviders > env**: если у выбранного провайдера есть `generationConfig.timeout`, env **не применяется** (`if (modelProvider?.generationConfig?.timeout !== void 0) return`).
- Default **`120000`** мс присутствует в том же чанке (SDK timeout до заголовков ответа).

## Вывод п.1

`QWEN_CODE_API_TIMEOUT_MS` **поддерживается** на установленной qwen 0.24.7 и переопределяет default 120000, если у активной модели нет своего `generationConfig.timeout` в modelProviders.

> Для doctor (реализация, п.8): корректный путь проверки — активный пакет по `active.json` (`~/.qwen/updates/npm/*/versions/<ver>/...`), а не `$(npm root -g)`. Строка `QWEN_CODE_API_TIMEOUT_MS` ищется в `chunks/`.
