// Боковая панель (9.5, 10.2): логотип + название/версия, пункты меню с
// иконками и счётчиками, оператор + выход внизу. Узкий режим (иконки) — CSS.
import { html } from '../html.js';
import { useStore } from '../store.js';
import { navigate } from '../router.js';
import { T } from '../i18n/ru.js';
import { post } from '../api.js';
import { Icon } from './icon-view.js';

const NAV = [
  { page: 'queue', icon: 'list-todo', label: T.nav.queue, countKey: 'queue' },
  { page: 'sessions', icon: 'users', label: T.nav.sessions, countKey: 'attention' },
  { page: 'servers', icon: 'server', label: T.nav.servers, countKey: 'servers' },
  { page: 'nodes', icon: 'monitor', label: T.nav.nodes, countKey: 'nodes' },
  { page: 'journal', icon: 'file-text', label: T.nav.journal },
  { page: 'monitoring', icon: 'activity', label: T.nav.monitoring },
  { page: 'notifications', icon: 'bell', label: T.nav.notifications, countKey: 'notifications' },
  { page: 'settings', icon: 'settings', label: T.nav.settings },
];

function navItem(item, s) {
  const active = s.page === item.page;
  const count = item.countKey ? (s.counts[item.countKey] || 0) : 0;
  const title = item.label + (count > 0 ? ' (' + count + ')' : '');
  return html`<div class="nav-item ${active ? 'is-active' : ''}" role="link"
    title=${title}
    onClick=${() => navigate(item.page)}>
    ${Icon({ name: item.icon })}
    <span class="nav-label">${item.label}</span>
    ${count > 0 ? html`<span class="nav-count is-alert">${count}</span>` : null}
  </div>`;
}

async function logout() {
  try { await post('/web/logout', {}); } catch (e) { /* ignore */ }
  location.href = '/web/login';
}

export function Sidebar() {
  const s = useStore();
  return html`<aside class="sidebar">
    <div class="brand" role="link" title=${T.brand.home} onClick=${() => navigate('queue')}>
      <div class="brand-mark">${Icon({ name: 'command' })}</div>
      <div>
        <div class="brand-name">${T.brand.name}</div>
        ${s.session.version ? html`<div class="brand-sub">v${s.session.version}</div>` : null}
      </div>
    </div>
    <nav class="nav" aria-label=${T.topbar.menu}>
      ${NAV.map((item) => navItem(item, s))}
    </nav>
    <div class="sidebar-foot">
      <div class="avatar">${Icon({ name: 'users', cls: 'icon--sm' })}</div>
      <div class="user-name">${s.session.actor || T.footer.operator}</div>
      <button class="icon-btn" type="button" title=${T.topbar.logout} onClick=${logout}>
        ${Icon({ name: 'menu' })}
      </button>
    </div>
  </aside>`;
}
