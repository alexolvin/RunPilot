// Экран «Сессии» (11.4): сессии, сгруппированные по состоянию (важные выше),
// с why.text для HOLD + секция «Вне runpilot» (8.3 X1): tmux-панели с кодером,
// запущенным без runpilot, с «Перезапустить под runpilot». Фильтр «Показать закрытые»
// (5.4): GONE-сессии с кнопкой «Удалить». Данные — из store (SSE).
import { html } from '../html.js';
import { useState } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { post, del } from '../api.js';
import { T } from '../i18n/ru.js';
import { sessionStateLabel, whyText } from '../labels.js';
import { EmptyState } from '../components/empty-state.js';
import { SessionRow } from '../components/session-row.js';
import { Section } from '../components/section.js';
import { Button } from '../components/button.js';
import { Dialog } from '../components/dialog.js';

// Порядок групп: требующие внимания и работающие — выше. GONE — отдельный
// фильтр «Показать закрытые» (из store.closed), не в основном списке.
const ORDER = ['HOLD', 'RUNNING', 'DISPATCHING', 'QUEUED', 'DETACHED', 'IDLE'];

export function SessionsPage() {
  const s = useStore();
  // 8.3 X1: панель на подтверждении adopt (или null).
  const [adopt, setAdopt] = useState(null);
  const [busy, setBusy] = useState(false);
  // 5.4: фильтр «Показать закрытые».
  const [showClosed, setShowClosed] = useState(false);

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }
  const groups = ORDER
    .map((st) => ({ st, list: s.sessions.filter((x) => x.state === st) }))
    .filter((g) => g.list.length > 0);
  const unmanaged = s.unmanaged || [];

  // GONE-список: живые (из s.sessions по SSE) + сохранённые (store.closed).
  const closedAll = (() => {
    const live = s.sessions.filter((x) => x.state === 'GONE');
    const seen = new Set(live.map((x) => x.sid));
    const merged = live.slice();
    for (const c of s.closed || []) {
      if (!seen.has(c.sid)) merged.push(c);
    }
    return merged;
  })();

  const toggleClosed = () => {
    const next = !showClosed;
    setShowClosed(next);
    if (next) actions.loadClosed();
  };
  const doAdopt = () => {
    setBusy(true);
    // pane_id — литерал вида «%14»: без encodeURIComponent сервер декодирует
    // «%14» как байт 0x14 и не находит панель (NOT_FOUND).
    post('/api/v1/unmanaged/' + encodeURIComponent(adopt.host) + '/' +
      encodeURIComponent(adopt.pane_id) + '/adopt', {})
      .then((r) => {
        const d = r.data || {};
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.sessions.unmanaged.adoptDone + ': ' + (d.name || adopt.session || adopt.pane_id) });
          window.__runpilotResync();
        } else {
          actions.addToast({ kind: 'error', text: d.detail || d.code || ('HTTP ' + r.status) });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }))
      .finally(() => { setBusy(false); setAdopt(null); });
  };
  const doDelete = (sid) => {
    del('/api/v1/sessions/' + sid)
      .then((r) => {
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.session.deleteDone });
          actions.removeClosed(sid);
        } else {
          const d = r.data || {};
          actions.addToast({ kind: 'error', text: d.detail || d.code || ('HTTP ' + r.status) });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  };

  if (!s.sessions.length && !unmanaged.length && !closedAll.length) {
    return html`<div class="page">
      <${EmptyState} icon="users" title=${T.empty.sessions.title} text=${T.empty.sessions.text}
        actionLabel=${T.empty.sessions.action} onAction=${() => actions.openDialog('new-session')} />
    </div>`;
  }
  return html`<div class="page">
    ${groups.map((g) => html`<${Section} title=${sessionStateLabel(s.meta, g.st)}
      count=${g.list.length}>
      <div class="srow-list">
        ${g.list.map((x) => SessionRow({ s: x, meta: s.meta,
          why: g.st === 'HOLD' ? whyText(s.meta, 'hold', x.hold_reason) : '' }))}
      </div>
    </${Section}>`)}
    ${unmanaged.length ? html`<${Section} title=${T.sessions.unmanaged.title} count=${unmanaged.length}>
      <p class="srow-hint">${T.sessions.unmanaged.hint}</p>
      <div class="srow-list">
        ${unmanaged.map((u) => html`<div class="srow">
          <div class="srow-main">
            <span class="srow-name" title=${u.pane_id}>${u.session || u.pane_id}</span>
            ${u.dir ? html`<span class="srow-sub mono">${u.dir}</span>` : null}
            <span class="srow-sub">${u.host}</span>
          </div>
          <div class="srow-side">
            <${Button} variant="secondary" label=${T.sessions.unmanaged.adopt}
              onClick=${() => setAdopt(u)} />
          </div>
        </div>`)}
      </div>
    </${Section}>` : null}
    <label class="srow-toggle">
      <input type="checkbox" checked=${showClosed} onChange=${toggleClosed} />
      ${T.sessions.showClosed}
    </label>
    ${showClosed ? (closedAll.length ? html`<${Section} title=${T.sessions.closedTitle} count=${closedAll.length}>
      <div class="srow-list">
        ${closedAll.map((x) => SessionRow({ s: x, meta: s.meta,
          onDelete: () => doDelete(x.sid) }))}
      </div>
    </${Section}>` : html`<p class="srow-hint">${T.sessions.closedEmpty}</p>`) : null}
    ${adopt ? html`<${Dialog} title=${T.sessions.unmanaged.adopt + ': ' + (adopt.session || adopt.pane_id)}
      onClose=${() => setAdopt(null)}
      footer=${html`
        <${Button} variant="secondary" label=${T.dialog.close} onClick=${() => setAdopt(null)} />
        <${Button} variant="danger" label=${T.sessions.unmanaged.adoptBtn} loading=${busy}
          onClick=${doAdopt} />`}>
      <p class="dialog-note">${T.sessions.unmanaged.adoptWarn}</p>
      ${adopt.dir ? html`<p class="dialog-note mono">${adopt.dir} · ${adopt.host}</p>` : null}
    </${Dialog}>` : null}
  </div>`;
}
