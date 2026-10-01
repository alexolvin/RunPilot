// Подписи кодов (из GET /api/v1/meta) + форматирование чисел/времени (W4).
// Кириллица — только в i18n/ru.js (R2); здесь — только ссылки на T и числа из
// constants.js. why.text (HOLD → hold_reason, QUEUED → ineligible) — по разделу 3.3.
import { T } from './i18n/ru.js';
import { FMT } from './constants.js';

function findEnum(list, code) {
  for (const e of list || []) {
    if (e.code === code) return e.label;
  }
  return '';
}

// Подписи состояний/классов из метаданных (R3: текст приходит из meta).
export function sessionStateLabel(meta, code) {
  return findEnum(meta && meta.sessions, code) || code || '';
}
export function serverStateLabel(meta, code) {
  return findEnum(meta && meta.servers, code) || code || '';
}
export function classLabel(meta, code) {
  return findEnum(meta && meta.classes, code) || code || '';
}
export function paneStateLabel(meta, code) {
  return findEnum(meta && meta.panes, code) || code || '';
}
export function severityLabel(meta, code) {
  return findEnum(meta && meta.severities, code) || code || '';
}
export function holdLabel(meta, code) {
  return (meta && meta.hold_reasons && meta.hold_reasons[code]) || code || '';
}
export function ineligibleLabel(meta, code) {
  return (meta && meta.ineligible && meta.ineligible[code]) || code || '';
}

// whyText — текст «почему» (раздел 3.3). kind: 'hold' | 'ineligible'.
export function whyText(meta, kind, code) {
  if (!code) return '';
  return kind === 'hold' ? holdLabel(meta, code) : ineligibleLabel(meta, code);
}

// --- Форматирование (числа — constants.js, единицы — i18n/ru.js) ---

// round2 — не более двух знаков после точки: «128.46», «42» (без «42.00»).
function round2(n) {
  return Number(n.toFixed(FMT.round2Digits));
}

export function fmtNum(n) {
  return (n === null || n === undefined || n === '') ? T.units.none : String(n);
}
export function fmtPct(n) {
  return n === null || n === undefined ? T.units.none : `${round2(n)}${T.units.pct}`;
}
export function fmtTokS(n) {
  return n === null || n === undefined ? T.units.none : `${round2(n)} ${T.units.tokS}`;
}
export function fmtW(n) {
  return n === null || n === undefined ? T.units.none : `${round2(n)} ${T.units.w}`;
}
export function fmtMS(n) {
  return n === null || n === undefined ? T.units.none : `${n} ${T.units.ms}`;
}
// Часовая длительность из секунд: «1ч 5м», «12м 30с», «45с».
export function fmtDur(totalSec) {
  if (totalSec === null || totalSec === undefined) return T.units.none;
  const s = Math.round(totalSec);
  const h = Math.floor(s / FMT.secPerHour);
  const m = Math.floor((s % FMT.secPerHour) / FMT.secPerMin);
  const sec = s % FMT.secPerMin;
  if (h > 0) return `${h}${T.units.hour} ${m}${T.units.minute}`;
  if (m > 0) return `${m}${T.units.minute} ${sec}${T.units.sec}`;
  return `${sec}${T.units.sec}`;
}
// Время из RFC3339 / unix — HH:MM:SS (локальное).
export function fmtTime(value) {
  if (!value) return T.units.none;
  const d = typeof value === 'number' ? new Date(value * FMT.msPerSec) : new Date(value);
  if (Number.isNaN(d.getTime())) return T.units.none;
  const p = (x) => (x < FMT.doubleDigit ? '0' : '') + x;
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}
