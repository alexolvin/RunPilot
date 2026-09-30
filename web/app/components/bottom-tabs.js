// Нижние вкладки (телефон, 10.3): иконки — Очередь · Сессии · Серверы · Журнал.
// «Ещё» — лист с остальными разделами (Узлы, Мониторинг, Уведомления,
// Настройки): на телефоне боковой панели нет, лист — единственная дорога.
import { html } from '../html.js';
import { useState } from 'preact/hooks';
import { useStore } from '../store.js';
import { navigate } from '../router.js';
import { T } from '../i18n/ru.js';
import { Icon } from './icon-view.js';
import { Dialog } from './dialog.js';
import { Button } from './button.js';

const TABS = [
  { page: 'queue', icon: 'list-todo', label: T.nav.queue },
  { page: 'sessions', icon: 'users', label: T.nav.sessions },
  { page: 'servers', icon: 'server', label: T.nav.servers },
  { page: 'journal', icon: 'file-text', label: T.nav.journal },
];

// «Ещё»: разделы вне основных вкладок (иконки совпадают с сайдбаром).
const MORE_PAGES = [
  { page: 'nodes', icon: 'monitor', label: T.nav.nodes },
  { page: 'monitoring', icon: 'activity', label: T.nav.monitoring },
  { page: 'notifications', icon: 'bell', label: T.nav.notifications },
  { page: 'settings', icon: 'settings', label: T.nav.settings },
];

function MoreSheet({ onClose }) {
  return html`<${Dialog} title=${T.topbar.menu} onClose=${onClose}>
    <div class="more-list">
      ${MORE_PAGES.map((p) => html`
        <${Button} variant="secondary" label=${p.label}
          onClick=${() => { onClose(); navigate(p.page); }} />`)}
    </div>
  </${Dialog}>`;
}

export function BottomTabs() {
  const s = useStore();
  const [more, setMore] = useState(false);
  const moreActive = MORE_PAGES.some((p) => p.page === s.page);
  const tab = (t) => {
    const active = s.page === t.page;
    return html`<div class="tab ${active ? 'is-active' : ''}" role="link"
      title=${t.label} aria-label=${t.label}
      onClick=${() => navigate(t.page)}>
      ${Icon({ name: t.icon })}
    </div>`;
  };
  return html`<nav class="bottom-tabs" aria-label=${T.topbar.menu}>
    ${TABS.map((t) => tab(t))}
    ${html`<div class="tab ${moreActive ? 'is-active' : ''}" role="link"
      title=${T.nav.more} aria-label=${T.nav.more}
      onClick=${() => setMore(true)}>
      ${Icon({ name: 'menu' })}
    </div>`}
    ${more ? html`<${MoreSheet} onClose=${() => setMore(false)} />` : null}
  </nav>`;
}
