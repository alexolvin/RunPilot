// Верхняя панель (10.2): заголовок + точка статуса, живая сводка,
// «Новая сессия» (Очередь/Сессии; на телефоне — иконка +), уведомления,
// переключатель темы.
import { html } from '../html.js';
import { useStore, actions } from '../store.js';
import { stateColorVar } from '../state-style.js';
import { T } from '../i18n/ru.js';
import { navigate } from '../router.js';
import { Button } from './button.js';
import { Icon } from './icon-view.js';
import { ThemeToggle } from './theme-toggle.js';

// Живая сводка под заголовком (9.1:5) — по текущей странице.
function summaryFor(s) {
  if (!s.loaded) return '';
  switch (s.page) {
    case 'queue':
      return (s.servers.length || s.sessions.length)
        ? T.pages.summary.queue(s.counts.queue)
        : T.empty.queue.title;
    case 'sessions':
      return s.sessions.length ? T.pages.summary.sessions(s.sessions.length) : T.empty.sessions.title;
    case 'servers':
      return s.servers.length ? T.pages.summary.servers(s.servers.length) : T.empty.servers.title;
    case 'nodes':
      return s.nodes.length ? T.pages.summary.nodes(s.nodes.length) : T.empty.nodes.title;
    case 'notifications':
      return s.notifications.length ? `${s.counts.notifications} ${T.notifications.active.toLowerCase()}` : T.notifications.empty;
    default:
      return '';
  }
}

// ModeDot — статус системы точкой у заголовка (цвет = режим; подпись — в
// подсказке). Заменяет текстовую плашку: на телефоне надпись съедала место.
function ModeDot() {
  const s = useStore();
  const label = T.mode[s.mode] || s.mode;
  return html`<span class="mode-dot" title=${label}
    style=${{ '--tone': stateColorVar(s.mode) }}></span>`;
}

function NewSessionButton() {
  const s = useStore();
  const show = s.page === 'queue' || s.page === 'sessions';
  if (!show) return null;
  return Button({ variant: 'primary', label: T.topbar.newSession,
    onClick: () => actions.openDialog('new-session') });
}

// NewSessionIconBtn — «Новая сессия» на телефоне: иконка + в действиях
// топбара (текстовая кнопка на <768px скрыта CSS; на широких экранах
// иконка не показывается — .ns-new{display:none}).
function NewSessionIconBtn() {
  const s = useStore();
  const show = s.page === 'queue' || s.page === 'sessions';
  if (!show) return null;
  return html`<button class="icon-btn ns-new" type="button" title=${T.topbar.newSession}
    aria-label=${T.topbar.newSession}
    onClick=${() => actions.openDialog('new-session')}>
    ${Icon({ name: 'plus' })}
  </button>`;
}

// AddServerButton — «Добавить сервер» на вкладке «Серверы» (десктоп).
function AddServerButton() {
  const s = useStore();
  if (s.page !== 'servers') return null;
  return Button({ variant: 'primary', label: T.servers.add,
    onClick: () => actions.openDialog('add-server') });
}

// AddServerIconBtn — «Добавить сервер» на телефоне: иконка + (паттерн .ns-new).
function AddServerIconBtn() {
  const s = useStore();
  if (s.page !== 'servers') return null;
  return html`<button class="icon-btn ns-new" type="button" title=${T.servers.add}
    aria-label=${T.servers.add}
    onClick=${() => actions.openDialog('add-server')}>
    ${Icon({ name: 'plus' })}
  </button>`;
}

// ConnectNodeButton — «Подключить узел» на вкладке «Узлы» (десктоп).
function ConnectNodeButton() {
  const s = useStore();
  if (s.page !== 'nodes') return null;
  return Button({ variant: 'primary', label: T.nodes.connect,
    onClick: () => actions.openDialog('enroll-node') });
}

// ConnectNodeIconBtn — «Подключить узел» на телефоне: иконка + (паттерн .ns-new).
function ConnectNodeIconBtn() {
  const s = useStore();
  if (s.page !== 'nodes') return null;
  return html`<button class="icon-btn ns-new" type="button" title=${T.nodes.connect}
    aria-label=${T.nodes.connect}
    onClick=${() => actions.openDialog('enroll-node')}>
    ${Icon({ name: 'plus' })}
  </button>`;
}

function Notifications() {
  const s = useStore();
  const n = s.counts.notifications || 0;
  return html`<button class="icon-btn" type="button" title=${T.topbar.notifications}
    onClick=${() => navigate('notifications')}>
    ${Icon({ name: 'bell' })}
    ${n > 0 ? html`<span class="count">${n}</span>` : null}
  </button>`;
}

export function Topbar() {
  const s = useStore();
  const title = T.pages[s.page] || T.pages.queue;
  return html`<header class="topbar">
    <div class="page-head">
      <div class="page-title"><span class="page-title-text">${title}</span>${ModeDot()}</div>
      <div class="page-sub">${summaryFor(s)}</div>
    </div>
    <div class="topbar-actions">
      ${NewSessionButton()}
      ${NewSessionIconBtn()}
      ${AddServerButton()}
      ${AddServerIconBtn()}
      ${ConnectNodeButton()}
      ${ConnectNodeIconBtn()}
      ${Notifications()}
      ${ThemeToggle()}
    </div>
  </header>`;
}
