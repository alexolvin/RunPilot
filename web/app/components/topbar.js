// Верхняя панель (10.2): заголовок + точка статуса, живая сводка,
// «Новая сессия» (Очередь/Сессии; на телефоне — иконка +), уведомления,
// переключатель темы.
import { html } from '../html.js';
import { useStore, actions } from '../store.js';
import { stateColorVar } from '../state-style.js';
import { T } from '../i18n/ru.js';
import { navigate } from '../router.js';
import { get } from '../api.js';
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

function Notifications() {
  const s = useStore();
  const n = s.counts.notifications || 0;
  return html`<button class="icon-btn" type="button" title=${T.topbar.notifications}
    onClick=${() => navigate('notifications')}>
    ${Icon({ name: 'bell' })}
    ${n > 0 ? html`<span class="count">${n}</span>` : null}
  </button>`;
}

// BrowserViewButton — «Живой просмотр» (ТЗ Relay §8): открывает окно вкладки
// координатора. Запрашивает список вкладок и открывает первую.
function BrowserViewButton() {
  const open = async () => {
    try {
      const r = await get('/api/v1/browser');
      const tabs = (r.data && r.data.tabs) || [];
      if (!r.data || !r.data.running || tabs.length === 0) {
        actions.addToast({ kind: 'info', text: T.browserView.none });
        return;
      }
      actions.openBrowserView({ tab: tabs[0] });
    } catch {
      actions.addToast({ kind: 'error', text: T.browserView.errGeneric });
    }
  };
  return html`<button class="icon-btn" type="button" title=${T.browserView.title}
    aria-label=${T.browserView.title} onClick=${open}>
    ${Icon({ name: 'monitor' })}
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
      ${BrowserViewButton()}
      ${Notifications()}
      ${ThemeToggle()}
    </div>
  </header>`;
}
