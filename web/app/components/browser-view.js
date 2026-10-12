// Живой просмотр вкладки браузера (ТЗ Relay §8): оверлей со screencast-кадрами
// (JPEG, WS) + управление (клик, ввод текста). Единственный путь для входа на
// сайт оркестратора (captcha/2FA — только оператор).
//
// Транспорт: браузер ↔ координатор — /web/ws/browser/view?tab= (WS).
// Сервер первым шлёт JSON {type:"open"} или {type:"error",code}; дальше
// двоичные кадры (JPEG). Клиент шлёт JSON {type:"click",x,y} |
// {type:"text",text} | {type:"close"}.
import { html } from '../html.js';
import { useEffect, useRef, useState } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { UI, BP } from '../constants.js';
import { T } from '../i18n/ru.js';
import { Icon } from './icon-view.js';

// wsURL — ws(s)://<host>/web/ws/browser/view?tab=<tab> из location.
function wsURL(tab) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return proto + '://' + location.host + '/web/ws/browser/view?tab=' + encodeURIComponent(tab);
}

function errText(code) {
  if (code === 'BROWSER_OFF') return T.browserView.errBrowserOff;
  if (code === 'TAB_NOT_FOUND') return T.browserView.errTab;
  if (code === 'SCREENCAST_FAILED') return T.browserView.errScreencast;
  if (code === 'VIEW_UNAVAILABLE') return T.browserView.errUnavailable;
  return T.browserView.errGeneric;
}

// useIsPhone — (< 768) телефон: весь экран.
function useIsPhone() {
  const [isPhone, setIsPhone] = useState(false);
  useEffect(() => {
    const upd = () => setIsPhone(window.innerWidth < BP.tablet);
    upd();
    window.addEventListener('resize', upd);
    return () => window.removeEventListener('resize', upd);
  }, []);
  return isPhone;
}

// useBrowserView — WS-подключение: кадры → <img>, управление → JSON.
function useBrowserView(tab, onErr) {
  const imgRef = useRef(null);
  const wsRef = useRef(null);
  const openedRef = useRef(false);
  const onErrRef = useRef(onErr);
  onErrRef.current = onErr;
  const [status, setStatus] = useState('connecting');
  const [errMsg, setErrMsg] = useState('');
  const [frameW, setFrameW] = useState(0);

  useEffect(() => {
    if (!tab) return undefined;
    const ws = new WebSocket(wsURL(tab));
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        let m;
        try { m = JSON.parse(ev.data); } catch { return; }
        if (m.type === 'open') { openedRef.current = true; setStatus('live'); }
        else if (m.type === 'error') {
          setStatus('error'); setErrMsg(errText(m.code));
          if (onErrRef.current) onErrRef.current();
        }
        else if (m.type === 'closed') setStatus('closed');
      } else if (openedRef.current) {
        const img = imgRef.current;
        if (!img) return;
        const url = URL.createObjectURL(new Blob([ev.data], { type: 'image/jpeg' }));
        img.onload = () => URL.revokeObjectURL(img.src);
        img.src = url;
      }
    };
    const onFail = () => {
      if (!openedRef.current) { setStatus('error'); setErrMsg(T.browserView.errGeneric); }
      else setStatus('closed');
    };
    ws.onerror = onFail;
    ws.onclose = onFail;
    return () => {
      ws.onmessage = null;
      ws.onerror = null;
      ws.onclose = null;
      try { ws.close(); } catch { /* уже закрыто */ }
      wsRef.current = null;
      openedRef.current = false;
    };
  }, [tab]);

  const send = (obj) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN && openedRef.current) {
      ws.send(JSON.stringify(obj));
    }
  };

  return { imgRef, status, errMsg, frameW, setFrameW, send };
}

// BrowserViewPage — хост оверлея (рендерит, пока открыт просмотр).
export function BrowserViewPage() {
  const s = useStore();
  if (!s.browserView) return null;
  return html`<${BrowserViewOverlay} tab=${s.browserView.tab} url=${s.browserView.url || ''} />`;
}

function BrowserViewOverlay({ tab, url }) {
  const close = () => actions.closeBrowserView();
  const { imgRef, status, errMsg, send } = useBrowserView(tab, close);
  const isPhone = useIsPhone();
  const pct = UI.terminalViewportPct;

  // Клик: координаты <img> → CSS-пиксели вьюпорта (масштаб по размеру кадра).
  const onClick = (e) => {
    const img = imgRef.current;
    if (!img || !img.naturalWidth) return;
    const rect = img.getBoundingClientRect();
    const x = ((e.clientX - rect.left) / rect.width) * img.naturalWidth;
    const y = ((e.clientY - rect.top) / rect.height) * img.naturalHeight;
    send({ type: 'click', x, y });
  };

  // Ввод: печатные клавиши → insertText (кириллица поддерживается).
  const onKeyDown = (e) => {
    if (e.key && e.key.length === 1) {
      e.preventDefault();
      send({ type: 'text', text: e.key });
    }
  };

  return html`<div class="bv-overlay" onClick=${close}>
    <div class="bv-panel ${isPhone ? 'bv-panel--phone' : ''}"
      style=${{ width: isPhone ? '100%' : pct + '%', height: isPhone ? '100%' : pct + '%' }}
      onKeyDown=${onKeyDown} tabIndex=${0}
      onClick=${(e) => e.stopPropagation()}>
      <div class="bv-head">
        <h2 class="bv-title">${T.browserView.title}
          <span class="bv-tab mono">${tab}</span>
        </h2>
        ${url ? html`<span class="bv-url" title=${url}>${url}</span>` : null}
        ${status === 'connecting' ? html`<span class="bv-status connecting">${T.browserView.connecting}</span>` : null}
        ${status === 'closed' ? html`<span class="bv-status closed">${T.browserView.disconnected}</span>` : null}
        <button class="bv-done" type="button" onClick=${close}>${T.browserView.done}</button>
        <button class="bv-close" type="button" aria-label=${T.browserView.close} onClick=${close}>
          ${Icon({ name: 'x' })}
        </button>
      </div>
      <div class="bv-body">
        <img ref=${imgRef} class="bv-frame" alt="" onClick=${onClick} draggable=${false} />
        ${status === 'error' ? html`<div class="bv-err">${errMsg}</div>` : null}
        ${status === 'connecting' ? html`<div class="bv-hint">${T.browserView.hint}</div>` : null}
      </div>
    </div>
  </div>`;
}
