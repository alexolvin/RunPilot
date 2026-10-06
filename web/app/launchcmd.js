// Клиентский аналог internal/launchcmd: «команды для запуска» (заряд запуска —
// текст + токены клавиш). Источник истины — Go-пакет; здесь только лёгкое
// парсинг/проверка/формирование для UI композера. Токены в скобках не могут
// совпасть с текстом промпта, поэтому парсинг однозначен. Подписи кнопок — в
// i18n (T.session.keyLabels), здесь только токен + клавиша tmux (R2).
export const KEYS = [
  { token: '[Enter]', key: 'Enter' },
  { token: '[Up]', key: 'Up' },
  { token: '[Down]', key: 'Down' },
  { token: '[Left]', key: 'Left' },
  { token: '[Right]', key: 'Right' },
  { token: '[Space]', key: 'Space' },
  { token: '[Esc]', key: 'Escape' },
];
const TOKENS = KEYS.map((k) => k.token);

// parseTokens — разбор «команды для запуска» в токены (text | key).
export function parseTokens(s) {
  const out = [];
  let buf = '';
  const flush = () => { if (buf) { out.push({ kind: 'text', value: buf }); buf = ''; } };
  let i = 0;
  while (i < s.length) {
    let matched = null;
    for (const tok of TOKENS) {
      if (s.startsWith(tok, i)) { matched = tok; break; }
    }
    if (matched) {
      flush();
      const k = KEYS.find((k) => k.token === matched);
      out.push({ kind: 'key', value: k.key, token: matched });
      i += matched.length;
      continue;
    }
    buf += s[i];
    i += 1;
  }
  flush();
  return out;
}

// hasKey — есть ли хотя бы одна клавиша.
export function hasKey(s) { return parseTokens(s).some((t) => t.kind === 'key'); }
// hasEnter — есть ли клавиша Enter.
export function hasEnter(s) { return parseTokens(s).some((t) => t.kind === 'key' && t.key === 'Enter'); }
// isTextOnly — только литеральный текст (годится для немедленной вставки).
export function isTextOnly(s) { return !hasKey(s); }

// ensureTrailingEnter — гарантировать завершающий Enter: пусто → «[Enter]»,
// уже с Enter → без изменений, иначе добавить. Правый whitespace обрезается.
export function ensureTrailingEnter(s) {
  const t = s.replace(/[ \t\r\n]+$/, '');
  if (t === '') return '[Enter]';
  const toks = parseTokens(t);
  const last = toks[toks.length - 1];
  if (last && last.kind === 'key' && last.key === 'Enter') return t;
  return t + '[Enter]';
}

// plainText — только литеральные части (превью вставки без клавиш).
export function plainText(s) {
  return parseTokens(s).filter((t) => t.kind === 'text').map((t) => t.value).join('');
}
