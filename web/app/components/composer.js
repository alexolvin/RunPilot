// Композер «Команды для запуска» (раздел постановки в очередь). Заряд запуска —
// текст + токены клавиш ([Enter],[Up],[Down],[Left],[Right],[Space],[Esc]) —
// буферизуется в системе и воспроизводится узлом только при запуске (dispatch).
//
// Кнопки вставляют токен по курсору (если поле не активно — в конец).
// «Вставить» — немедленная вставка ЛИТЕРАЛЬНОГО текста в работающий кодер
// (без клавиш); запрещён, если команда содержит клавиши или сессия в очереди.
// «Вставить в очередь» — буфер + постановка в очередь (с завершающим Enter;
// пустое поле → просто Enter). «Отозвать из очереди» — снять с очереди.
import { html } from '../html.js';
import { useState, useRef, useEffect } from 'preact/hooks';
import { post } from '../api.js';
import { T } from '../i18n/ru.js';
import { Button } from './button.js';
import { KEYS, hasKey, ensureTrailingEnter } from '../launchcmd.js';

export function Composer({ sid, disabled, state, initialValue, onWithdrawn }) {
  const [value, setValue] = useState(initialValue || '');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState(null); // {code, text}
  const taRef = useRef(null);

  // Синхронизация при внешнем изменении initialValue (переоткрытие на правке).
  useEffect(() => { if (initialValue !== undefined) setValue(initialValue || ''); }, [initialValue]);

  const queued = state === 'QUEUED';

  // insertToken — вставить токен по курсору, или в конец (поле не активно).
  function insertToken(token) {
    const ta = taRef.current;
    if (!ta) { setValue((v) => v + token); return; }
    const start = ta.selectionStart != null ? ta.selectionStart : value.length;
    const end = ta.selectionEnd != null ? ta.selectionEnd : value.length;
    const next = value.slice(0, start) + token + value.slice(end);
    setValue(next);
    const pos = start + token.length;
    requestAnimationFrame(() => { ta.focus(); ta.setSelectionRange(pos, pos); });
  }

  // pasteNow — «Вставить» (немедленная вставка текста, без клавиш) → /paste.
  async function pasteNow() {
    if (busy) return;
    const text = value.trim();
    if (hasKey(value)) { setResult({ code: 'ERR', text: T.session.pasteBlockedKeys }); return; }
    if (queued) { setResult({ code: 'ERR', text: T.session.pasteBlockedQueued }); return; }
    if (!text) { setResult({ code: 'ERR', text: T.session.pasteEmpty }); return; }
    setBusy(true); setResult(null);
    const r = await post(`/api/v1/sessions/${encodeURIComponent(sid)}/paste`, { text, enqueue: false });
    setBusy(false);
    if (r.ok && r.data) {
      setResult({ code: r.data.code || '', text: r.data.text || '' });
      if (r.data.code === 'OK') setValue('');
    } else {
      setResult({ code: 'ERR', text: (r.data && (r.data.detail || r.data.text)) || ('HTTP ' + r.status) });
    }
  }

  // enqueueLaunch — «Вставить в очередь» (буфер + enqueue, завершающий Enter).
  async function enqueueLaunch() {
    if (busy) return;
    const cmd = ensureTrailingEnter(value);
    setBusy(true); setResult(null);
    const r = await post(`/api/v1/sessions/${encodeURIComponent(sid)}/launch-command`,
      { command: cmd, enqueue: true });
    setBusy(false);
    if (r.ok && r.data) {
      setResult({ code: r.data.code || 'OK', text: r.data.text || T.session.resultOK });
      window.__runpilotResync && window.__runpilotResync();
    } else {
      setResult({ code: 'ERR', text: (r.data && (r.data.detail || r.data.text)) || ('HTTP ' + r.status) });
    }
  }

  // withdraw — «Отозвать из очереди» (dequeue): сессия → IDLE, команды сохраняются.
  async function withdraw() {
    if (busy) return;
    setBusy(true); setResult(null);
    const r = await post(`/api/v1/sessions/${encodeURIComponent(sid)}/dequeue`, {});
    setBusy(false);
    if (r.ok) {
      setResult({ code: 'OK', text: T.session.withdrawDone });
      window.__runpilotResync && window.__runpilotResync();
      onWithdrawn && onWithdrawn();
    } else {
      setResult({ code: 'ERR', text: (r.data && r.data.detail) || ('HTTP ' + r.status) });
    }
  }

  // onKey — Enter = в очередь; Alt+Enter = вставить; Shift+Enter = новая строка.
  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey) { e.preventDefault(); enqueueLaunch(); }
    else if (e.key === 'Enter' && e.altKey && !e.shiftKey) { e.preventDefault(); pasteNow(); }
  }

  const off = disabled || busy;
  return html`<div class="composer composer-launch">
    <label class="composer-label">${T.session.launchField}</label>
    <textarea class="composer-input" placeholder=${T.session.composer}
      rows="2" value=${value} disabled=${disabled ? true : null}
      ref=${taRef}
      onInput=${(e) => setValue(e.target.value)}
      onKeyDown=${onKey}></textarea>
    <div class="composer-keys">
      ${KEYS.map((k) => html`<button type="button" class="key-btn"
        title=${k.token + ' (' + k.key + ')'}
        onClick=${() => insertToken(k.token)}>${T.session.keyLabels[k.key] || k.key}</button>`)}
      <span class="composer-keys-hint">${T.session.keysHint}</span>
    </div>
    <div class="composer-bar">
      <span class="composer-hint">${T.session.composerHint}</span>
      <span class="composer-actions">
        <${Button} variant="secondary" label=${T.session.paste} disabled=${off}
          onClick=${pasteNow} />
        <${Button} variant="primary" label=${T.session.pasteEnqueue} disabled=${off}
          loading=${busy}
          onClick=${enqueueLaunch} />
        ${queued ? html`<${Button} variant="danger" label=${T.session.withdrawQueue}
          disabled=${off} onClick=${withdraw} />` : null}
      </span>
    </div>
    ${result ? html`<div class="composer-result composer-result--${result.code === 'OK' ? 'ok' : 'err'}">${result.text}</div>` : null}
  </div>`;
}
