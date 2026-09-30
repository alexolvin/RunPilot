// Оболочка командной строки (раздел 12, v2): общий разбор/выполнение/подсказки
// через /api/v1/command (+ /complete) — тот же код, что Telegram. «/» фокусирует
// строку вне других полей; Tab/Enter — принять, ↑/↓ — выбрать, Esc — закрыть.
// Успех — тост, ошибка — под строкой (введённое сохраняется).
import { html } from '../html.js';
import { useRef, useEffect, useState } from 'preact/hooks';
import { T } from '../i18n/ru.js';
import { UI } from '../constants.js';
import { get, post } from '../api.js';
import { actions } from '../store.js';
import { Icon } from './icon-view.js';

export function CommandBar() {
  const inputRef = useRef(null);
  const [value, setValue] = useState('');
  const [sugg, setSugg] = useState([]);
  const [sel, setSel] = useState(-1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // «/» — фокус (вне INPUT/TEXTAREA).
  useEffect(() => {
    function onKey(e) {
      if (e.key !== '/') return;
      const t = e.target;
      const tag = t && t.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;
      e.preventDefault();
      if (inputRef.current) inputRef.current.focus();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Подсказки: дебаунс при вводе (только для команд «/»).
  useEffect(() => {
    if (!value.startsWith('/')) { setSugg([]); return; }
    const h = setTimeout(async () => {
      const r = await get(`/api/v1/command/complete?line=${encodeURIComponent(value)}`);
      if (r.ok && Array.isArray(r.data)) setSugg(r.data.slice(0, UI.maxSuggestions));
      else setSugg([]);
    }, UI.completeDebounceMs);
    return () => clearTimeout(h);
  }, [value]);

  async function execute(line) {
    if (!line.startsWith('/') || busy) return;
    setBusy(true);
    setError('');
    const r = await post('/api/v1/command', { line });
    setBusy(false);
    const code = (r.data && r.data.code) || 'ERROR';
    const text = (r.data && r.data.text) || (r.data && r.data.detail) || '';
    if (code === 'OK') {
      actions.addToast({ kind: 'info', text: text || T.session.resultOK });
      setValue('');
      setSugg([]);
      setSel(-1);
    } else {
      setError(text || code);
    }
  }

  function applySuggestion(i) {
    const s = sugg[i];
    if (!s) return;
    setValue(s.text);
    setSugg([]);
    setSel(-1);
  }

  function onKey(e) {
    if (e.key === 'Escape') { setSugg([]); setSel(-1); return; }
    if (sugg.length > 0) {
      if (e.key === 'ArrowDown') { e.preventDefault(); setSel((x) => (x + 1) % sugg.length); return; }
      if (e.key === 'ArrowUp') { e.preventDefault(); setSel((x) => (x - 1 + sugg.length) % sugg.length); return; }
      if (e.key === 'Tab') { e.preventDefault(); applySuggestion(sel >= 0 ? sel : 0); return; }
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (sel >= 0 && sugg[sel]) applySuggestion(sel);
      else execute(value);
    }
  }

  return html`<div class="command-bar">
    ${sugg.length > 0 ? html`<div class="command-suggest">
      ${sugg.map((s, i) => html`<div class="row ${i === sel ? 'is-active' : ''}"
        onMouseDown=${(e) => { e.preventDefault(); applySuggestion(i); }}>
        <span class="cmd">${s.text}</span>
        ${s.description ? html`<span class="desc">${s.description}</span>` : null}
      </div>`)}
    </div>` : null}
    <div class="command-input">
      ${Icon({ name: 'command' })}
      <input ref=${inputRef} type="text" placeholder=${T.command.placeholder}
        autocomplete="off" spellcheck="false" value=${value}
        onInput=${(e) => { setValue(e.target.value); setError(''); }}
        onKeyDown=${onKey} />
      <span class="command-hint">${T.command.hint}</span>
    </div>
    ${error ? html`<div class="command-error">${error}</div>` : null}
  </div>`;
}
