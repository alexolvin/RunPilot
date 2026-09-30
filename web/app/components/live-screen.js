// Живой экран кодера (11.3/15.4, v2): только чтение. Кадр — из подписки
// SSE ?screens=<sid> (события SCREEN, раз в web.screen_interval_ms); первый
// кадр — GET /sessions/{sid}/screen. Отдельный EventSource (не общий поток):
// состояние остаётся за общим SSE (R3), сюда приходит только SCREEN.
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { get } from '../api.js';
import { T } from '../i18n/ru.js';

// Убирает ANSI-послеокраски (CSI) из снимка capture-pane -e. Цифры — как
// \p{Nd} (без числовых литералов в JS — R2/lintweb).
const ANSI_RE = /\x1b\[[\p{Nd};?]*[ -\/]*[@-~]/gu;
function stripAnsi(s) {
  return (s || '').replace(ANSI_RE, '');
}

export function LiveScreen({ sid }) {
  const [text, setText] = useState('');
  const [status, setStatus] = useState('loading'); // loading|live|empty|error

  useEffect(() => {
    let closed = false;
    // Первый кадр (разовый снимок) до первого SCREEN из потока.
    get(`/api/v1/sessions/${encodeURIComponent(sid)}/screen`).then((r) => {
      if (closed) return;
      const t = r.ok && r.data ? stripAnsi(r.data.text_ansi) : '';
      setText(t);
      setStatus(t ? 'live' : 'empty');
    }).catch(() => { if (!closed) setStatus('error'); });

    const es = new EventSource(`/api/v1/events?screens=${encodeURIComponent(sid)}`);
    es.onmessage = (e) => {
      let ev;
      try { ev = JSON.parse(e.data); } catch (err) { return; }
      if (ev.kind === 'SCREEN' && (!ev.sid || ev.sid === sid) && ev.payload) {
        const t = stripAnsi(ev.payload.text_ansi);
        setText(t);
        setStatus(t ? 'live' : 'empty');
      }
    };
    es.onerror = () => {
      // EventSource переподключится сам; пока показываем последний кадр.
      if (status !== 'live' && status !== 'loading') setStatus('error');
    };
    return () => { closed = true; es.close(); };
  }, [sid]);

  return html`<div class="livescreen-wrap">
    <div class="livescreen-head">
      <span class="livescreen-title">${T.session.screen}</span>
      <span class="livescreen-live livescreen-live--${status}">${
        status === 'live' ? T.session.screenLive : T.session.screenEmpty
      }</span>
    </div>
    <pre class="livescreen">${status === 'loading' && text === '' ? '…' : text}</pre>
  </div>`;
}
