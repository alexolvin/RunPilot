// Центр уведомлений (11.11): «Активные» / «История», прочитать (одно/все).
// Список — разовый GET /api/v1/notifications; счётчик вкладки — клиентский.
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post } from '../api.js';
import { T } from '../i18n/ru.js';
import { severityLabel, fmtTime } from '../labels.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';
import { EmptyState } from '../components/empty-state.js';
import { navigate } from '../router.js';

const SEV_TONE = {
  CRIT: 'var(--color-danger)',
  WARN: 'var(--color-warning)',
  INFO: 'var(--color-info)',
};

function NotifRow(props) {
  const n = props.n;
  const meta = props.meta;
  const tone = SEV_TONE[n.severity] || 'var(--color-text-muted)';
  return html`<div class="notif ${n.read_at ? '' : 'is-active'}">
    <${Badge} tone=${tone} label=${severityLabel(meta, n.severity)} />
    <div class="notif-main">
      <div class="notif-title">${n.title}</div>
      <div class="notif-sub">${(n.sid || n.server || '') + ' · ' + fmtTime(n.last_ts)}</div>
    </div>
    ${n.read_at ? null
      : html`<${Button} variant="text" label=${T.notifications.read}
          onClick=${() => props.onRead(n.id)} />`}
  </div>`;
}

export function NotificationsPage() {
  const s = useStore();
  const [all, setAll] = useState(false);

  function load() {
    get('/api/v1/notifications' + (all ? '?all=true' : ''))
      .then((r) => { if (r.ok && r.data) actions.setNotifications(r.data); });
  }
  useEffect(() => { load(); }, [all]);

  function readOne(id) { post('/api/v1/notifications/' + id + '/read').then(load); }
  function readAll() { post('/api/v1/notifications/read-all').then(load); }

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }
  const list = s.notifications;
  if (!list.length) {
    return html`<div class="page">
      <${EmptyState} icon="bell" title=${T.empty.notifications.title} text=${T.empty.notifications.text}
        actionLabel=${T.empty.notifications.action} onAction=${() => navigate('queue')} />
    </div>`;
  }
  return html`<div class="page">
    <div class="tabs">
      <button class="tab-btn ${!all ? 'is-active' : ''}" type="button"
        onClick=${() => setAll(false)}>${T.notifications.active}</button>
      <button class="tab-btn ${all ? 'is-active' : ''}" type="button"
        onClick=${() => setAll(true)}>${T.notifications.history}</button>
      ${!all ? html`<${Button} variant="text" label=${T.notifications.readAll}
        onClick=${readAll} />` : null}
    </div>
    <div class="notif-list">
      ${list.map((n) => NotifRow({ n, meta: s.meta, onRead: readOne }))}
    </div>
  </div>`;
}
