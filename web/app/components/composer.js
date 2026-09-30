// Композер карточки (13.1): Enter — вставить и в очередь, Alt+Enter — только
// вставить, Shift+Enter — новая строка. Текст идёт на /paste (побайто,
// многострочный) — не через командную строку.
import { html } from '../html.js';
import { useState } from 'preact/hooks';
import { post } from '../api.js';
import { T } from '../i18n/ru.js';
import { Button } from './button.js';

export function Composer({ sid, disabled }) {
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState(null); // {code, text}

  async function submit(enqueue) {
    const text = value.replace(/\s+$/, '');
    if (busy || !text) return;
    setBusy(true);
    setResult(null);
    const r = await post(`/api/v1/sessions/${encodeURIComponent(sid)}/paste`,
      { text, enqueue });
    setBusy(false);
    if (r.ok && r.data) {
      setResult({ code: r.data.code || '', text: r.data.text || '' });
      if (r.data.code === 'OK') setValue('');
    } else {
      const code = (r.data && r.data.code) || '';
      const msg = (r.data && r.data.text) || (r.data && r.data.detail) || '';
      setResult({ code: code || 'ERROR', text: msg });
    }
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.altKey) {
      e.preventDefault();
      submit(true);
    } else if (e.key === 'Enter' && e.altKey && !e.shiftKey) {
      e.preventDefault();
      submit(false);
    }
  }

  const off = disabled || busy;
  return html`<div class="composer">
    <textarea class="composer-input" placeholder=${T.session.composer}
      rows="2" value=${value} disabled=${disabled ? true : null}
      onInput=${(e) => setValue(e.target.value)}
      onKeyDown=${onKey}></textarea>
    <div class="composer-bar">
      <span class="composer-hint">${T.session.composerHint}</span>
      <span class="composer-actions">
        <${Button} variant="secondary" label=${T.session.paste} disabled=${off}
          loading=${busy && false}
          onClick=${() => submit(false)} />
        <${Button} variant="primary" label=${T.session.pasteEnqueue} disabled=${off}
          loading=${busy}
          onClick=${() => submit(true)} />
      </span>
    </div>
    ${result ? html`<div class="composer-result composer-result--${result.code === 'OK' ? 'ok' : 'err'}">${result.text}</div>` : null}
  </div>`;
}
