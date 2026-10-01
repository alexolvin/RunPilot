// Терминал в браузере (13.4): xterm.js поверх PTY (tmux attach узла).
// Два способа показа:
//   * оверлей (TerminalPage) — «Открыть терминал» с карточки/списка,
//     десктоп 85% экрана, телефон — весь экран;
//   * inline (TerminalInline) — прямо в карточке сессии (доп-3i): ввод и
//     курсорные клавиши/Enter — без перехода в оверлей, шрифт меньше.
// Транспорт (13.4): браузер ↔ координатор — RAW-байты. Координатор первым
// шлёт JSON {"type":"open","chan"} или {"type":"error","code"}; дальше —
// только binary-кадры. Ввод (xterm onData) → binary в PTY. Нажатия клавиш не
// логируются (аудит — только TERM_OPEN/CLOSE).
import { html } from '../html.js';
import { useEffect, useRef, useState } from 'preact/hooks';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { useStore, actions } from '../store.js';
import { UI, BP } from '../constants.js';
import { T } from '../i18n/ru.js';
import { Icon } from './icon-view.js';

// wsURL — ws(s)://<host>/web/ws/term/<sid> из текущего location (cookie — сам
// браузер; проверка Origin = web.public_url — на сервере, handleTerminal).
function wsURL(sid) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  return proto + '://' + location.host + '/web/ws/term/' + sid;
}

// errText — код ошибки координатора → подпись (R2: кириллица только в i18n).
function errText(code) {
  if (code === 'TERMINAL_LIMIT') return T.terminal.errLimit;
  if (code === 'PANE_NOT_FOUND') return T.terminal.errPane;
  if (code === 'TERM_UNAVAILABLE') return T.terminal.errUnavailable;
  return T.terminal.errGeneric;
}

// KEY_SEQ — байты клавиш строки телефона (raw в PTY).
const KEY_SEQ = {
  esc: '\u001b',
  tab: '\u0009',
  up: '\u001b[A',
  down: '\u001b[B',
  left: '\u001b[D',
  right: '\u001b[C',
  enter: '\r',
};

// useIsPhone — (< 768) телефон: весь экран + строка клавиш над клавиатурой.
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

// useXterm — подключение xterm + WS: один раз на (sid, fontSize). Возвращает
// ref контейнера, статус, текст ошибки, состояние Ctrl и sendKey (строка
// клавиш). onErr — вызывается при ошибке открытия (оверлей закрывается;
// inline просто показывает ошибку).
function useXterm(sid, fontSize, onErr) {
  const boxRef = useRef(null);
  const wsRef = useRef(null);
  const openedRef = useRef(false);
  const ctrlRef = useRef(false);
  const encRef = useRef(new TextEncoder());
  const onErrRef = useRef(onErr);
  onErrRef.current = onErr;
  const [status, setStatus] = useState('connecting');
  const [errMsg, setErrMsg] = useState('');
  const [ctrlOn, setCtrlOn] = useState(false);

  useEffect(() => {
    const box = boxRef.current;
    if (!box) return undefined;
    const enc = encRef.current;
    const term = new Terminal({
      convertEol: false,
      cursorBlink: true,
      fontSize,
      scrollback: UI.termScrollback,
      fontFamily: 'IBM Plex Mono, ui-monospace, monospace',
      // Прозрачный фон: и фон (.term-body: var(--color-screen-bg)), и цвет
      // текста (color: var(--color-screen-text)) дают CSS из tokens.css —
      // xterm наследует color от контейнера (theme.foreground v6 не применяется).
      theme: { background: 'transparent' },
    });
    // FitAddon (W9): контейнер .term-body имеет flex:1 (высота известна), но
    // xterm по умолчанию держит дефолтные 24 строки — терминал занимал ~2/3
    // панели. fit() считает cols/rows под текущий размер; ResizeObserver
    // пересчитывает при изменении (включая первый layout).
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(box);
    const doFit = () => { try { fit.fit(); } catch { /* контейнер ещё без размера */ } };
    doFit();
    const ro = new ResizeObserver(doFit);
    ro.observe(box);
    term.focus();

    const send = (bytes) => {
      const ws = wsRef.current;
      if (ws && ws.readyState === WebSocket.OPEN && openedRef.current) ws.send(bytes);
    };
    const onData = (data) => {
      if (ctrlRef.current) {
        ctrlRef.current = false;
        setCtrlOn(false);
        const c = data.charCodeAt(0);
        if (c >= 0x61 && c <= 0x7a) { // a..z → Ctrl+буква (Ctrl+C = 0x03)
          send(enc.encode(String.fromCharCode(c - 0x60)));
          return;
        }
      }
      send(enc.encode(data));
    };
    term.onData(onData);

    const ws = new WebSocket(wsURL(sid));
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
      } else if (openedRef.current) {
        term.write(new Uint8Array(ev.data));
      }
    };
    const onFail = () => {
      if (!openedRef.current) { setStatus('error'); setErrMsg(T.terminal.errGeneric); }
      else setStatus('closed');
    };
    ws.onerror = onFail;
    ws.onclose = onFail;

    return () => {
      ro.disconnect();
      ws.onmessage = null;
      ws.onerror = null;
      ws.onclose = null;
      try { ws.close(); } catch { /* уже закрыто */ }
      wsRef.current = null;
      openedRef.current = false;
      term.dispose();
    };
  }, [sid, fontSize]);

  // Строка клавиш (телефон): raw-байты в PTY; Ctrl — модификатор-переключатель.
  const sendKey = (k) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN || !openedRef.current) return;
    if (k === 'ctrl') {
      ctrlRef.current = !ctrlRef.current;
      setCtrlOn(ctrlRef.current);
      return;
    }
    ws.send(encRef.current.encode(KEY_SEQ[k]));
    if (ctrlRef.current) { ctrlRef.current = false; setCtrlOn(false); }
  };

  return { boxRef, status, errMsg, ctrlOn, sendKey };
}

// KeyRow — строка клавиш (телефон): Esc/Tab/Ctrl/стрелки/Enter.
function KeyRow({ sendKey, ctrlOn }) {
  return html`<div class="term-keyrow">
    <button class="term-key" type="button" title=${T.terminal.keyEsc} onClick=${() => sendKey('esc')}>Esc</button>
    <button class="term-key" type="button" title=${T.terminal.keyTab} onClick=${() => sendKey('tab')}>Tab</button>
    <button class="term-key ${ctrlOn ? 'is-active' : ''}" type="button"
      title=${T.terminal.keyCtrl} onClick=${() => sendKey('ctrl')}>Ctrl</button>
    <button class="term-key" type="button" title=${T.terminal.keyUp} onClick=${() => sendKey('up')}>↑</button>
    <button class="term-key" type="button" title=${T.terminal.keyDown} onClick=${() => sendKey('down')}>↓</button>
    <button class="term-key" type="button" title=${T.terminal.keyLeft} onClick=${() => sendKey('left')}>←</button>
    <button class="term-key" type="button" title=${T.terminal.keyRight} onClick=${() => sendKey('right')}>→</button>
    <button class="term-key" type="button" title=${T.terminal.keyEnter} onClick=${() => sendKey('enter')}>Enter</button>
  </div>`;
}

function statusLabel(status) {
  if (status === 'connecting') return T.terminal.connecting;
  if (status === 'closed') return T.terminal.disconnected;
  return '';
}

// TerminalPage — хост оверлея (рендерит, пока открыт терминал).
export function TerminalPage() {
  const s = useStore();
  if (!s.terminal) return null;
  return html`<${TerminalOverlay} sid=${s.terminal} />`;
}

function TerminalOverlay({ sid }) {
  const close = () => actions.closeTerminal();
  const { boxRef, status, errMsg, ctrlOn, sendKey } = useXterm(sid, UI.termFontSizePx, close);
  const isPhone = useIsPhone();
  const pct = UI.terminalViewportPct;
  return html`<div class="term-overlay" onClick=${close}>
    <div class="term-panel ${isPhone ? 'term-panel--phone' : ''}"
      style=${{ width: isPhone ? '100%' : pct + '%', height: isPhone ? '100%' : pct + '%' }}
      onClick=${(e) => e.stopPropagation()}>
      <div class="term-head">
        <h2 class="term-title">${T.terminal.title}
          <span class="term-sid mono">${sid}</span>
        </h2>
        ${statusLabel(status) ? html`<span class="term-status ${status}">${statusLabel(status)}</span>` : null}
        <button class="term-close" type="button" aria-label=${T.terminal.close}
          onClick=${close}>
          ${Icon({ name: 'x' })}
        </button>
      </div>
      <div class="term-body" ref=${boxRef}></div>
      ${status === 'error' ? html`<div class="term-err">${errMsg}</div>` : null}
      ${isPhone ? html`<${KeyRow} sendKey=${sendKey} ctrlOn=${ctrlOn} />` : null}
    </div>
  </div>`;
}

// TerminalInline — терминал прямо в карточке сессии (доп-3i): ввод, курсорные
// клавиши и Enter — без оверлея; шрифт меньше (UI.termInlineFontSizePx).
// Ошибка открытия (панель отсутствует, узел офлайн, лимит) — подпись под
// контейнером, карточка остаётся.
export function TerminalInline({ sid }) {
  const { boxRef, status, errMsg, ctrlOn, sendKey } = useXterm(sid, UI.termInlineFontSizePx);
  const isPhone = useIsPhone();
  return html`<div class="term-inline">
    <div class="term-head term-inline-head">
      <span class="livescreen-title">${T.session.screen}</span>
      ${statusLabel(status) ? html`<span class="term-status ${status}">${statusLabel(status)}</span>`
        : status === 'live' ? html`<span class="livescreen-live livescreen-live--live">${T.session.screenLive}</span>`
        : null}
    </div>
    <div class="term-body term-inline-body" ref=${boxRef}></div>
    ${status === 'error' ? html`<div class="term-err">${errMsg}</div>` : null}
    ${isPhone ? html`<${KeyRow} sendKey=${sendKey} ctrlOn=${ctrlOn} />` : null}
  </div>`;
}
