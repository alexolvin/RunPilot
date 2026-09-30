import { SERVER_COLORS } from './constants.js';

// Соответствие кодов состояний/классов/серверов/режимов токенам цвета
// (v2 раздел 9.4). EДИНСТВЕННЫЙ файл этого соответствия. Подписи кодов
// приходят из GET /api/v1/meta (R3); здесь — только код → токен цвета.
// Один код — один цвет на всех экранах (9.4).

// Код → имя цветового токена (без префикса --color-).
const TOKEN = {
  // Сессия.
  RUNNING: 'success',
  DISPATCHING: 'accent',
  QUEUED: 'accent',
  DETACHED: 'info',
  HOLD: 'danger',
  IDLE: 'text-muted',
  GONE: 'text-muted',
  // Панель.
  PROMPT: 'attention',
  UNMANAGED: 'warning',
  // Класс.
  RESUME: 'violet',
  HIGH: 'danger',
  NORMAL: 'warning',
  LOW: 'text-muted',
  // Сервер.
  UP: 'success',
  DRAINING: 'warning',
  QUARANTINED: 'attention',
  DOWN: 'danger',
  MODEL_PROBLEM: 'danger',
  KEY_MISSING: 'danger',
  DISABLED: 'text-muted',
  REMOVING: 'text-muted',
  // Режим (9.4).
  NORMAL: 'success',
  PAUSED: 'warning',
  EMERGENCY: 'danger',
  SAFE_MODE: 'info',
};

// stateColorVar(code) → 'var(--color-<токен>)' для инлайн-переменной --tone.
export function stateColorVar(code) {
  const t = TOKEN[code] || 'text-muted';
  return 'var(--color-' + t + ')';
}

// serverColorVar(index) → цвет сервера по color_index (9.2: --server-1..5).
export function serverColorVar(index) {
  const i = (index || 0) % SERVER_COLORS + 1;
  return 'var(--server-' + i + ')';
}
