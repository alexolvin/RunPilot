// Экран «Очередь» (11.2): карточки серверов, «В работе», «Очередь» (с WHY),
// «Требуют внимания» (HOLD + why.text) и сводка «Сегодня». Живое — из store
// (SSE); строки очереди и сводка дня — разовый GET при входе на экран.
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post } from '../api.js';
import { T } from '../i18n/ru.js';
import { stateColorVar } from '../state-style.js';
import { classLabel, whyText, fmtDur } from '../labels.js';
import { UI } from '../constants.js';
import { ServerCard } from '../components/server-card.js';
import { SessionRow } from '../components/session-row.js';
import { EmptyState } from '../components/empty-state.js';
import { Stat } from '../components/stat.js';
import { Section, SectionEmpty } from '../components/section.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';

// W7 (8.2, J9/J11): статусы внешних кодеров.
const EXT_STATUS = { ACTIVE: 'stActive', KILLED: 'stKilled', GONE: 'stGone', IGNORED: 'stIgnored' };
const EXT_TONE = { ACTIVE: 'var(--color-warning)', KILLED: 'var(--color-danger)', GONE: 'var(--color-text-muted)', IGNORED: 'var(--color-text-muted)' };

// clearQueue — «Очистить очередь» (O5/J12): команда /clear-queue, затем
// всплывающее сообщение с кнопкой «Отменить» (POST /queue/undo).
function clearQueue() {
  post('/api/v1/command', { line: '/clear-queue' }).then((r) => {
    if (!r.ok) {
      actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
      return;
    }
    actions.addToast({
      kind: 'info', text: T.queue.cleared,
      action: {
        label: T.queue.undo,
        fn: () => post('/api/v1/queue/undo').then((u) => {
          if (u.ok) {
            actions.addToast({ kind: 'success', text: T.queue.undoDone });
          } else {
            actions.addToast({ kind: 'error', text: T.queue.undoExpired });
          }
        }).catch(() => actions.addToast({ kind: 'error', text: 'network' })),
      },
    });
  }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
}

export function QueuePage() {
  const s = useStore();
  const [queueRows, setQueueRows] = useState([]);
  const [mon, setMon] = useState(null);
  const [externals, setExternals] = useState([]);

  useEffect(() => {
    get('/api/v1/queue').then((r) => { if (r.ok && r.data) setQueueRows(r.data); });
    get('/api/v1/monitoring').then((r) => { if (r.ok && r.data) setMon(r.data); });
    get('/api/v1/externals').then((r) => {
      if (r.ok && r.data) setExternals(r.data.externals || []);
    });
  }, []);

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }

  const running = s.sessions.filter((x) => x.state === 'RUNNING' || x.state === 'DISPATCHING');
  const hold = s.sessions.filter((x) => x.state === 'HOLD');
  const hasAny = s.servers.length > 0 || s.sessions.length > 0;

  if (!hasAny) {
    return html`<div class="page">
      <${EmptyState} icon="list-todo" title=${T.empty.queue.title} text=${T.empty.queue.text}
        actionLabel=${T.empty.queue.action} onAction=${() => actions.openDialog('new-session')} />
    </div>`;
  }

  const shownRunning = running.slice(0, UI.queueWorkMax);

  return html`<div class="page">
    <${Section} title=${T.queue.servers} count=${s.servers.length}>
      ${s.servers.length
        ? html`<div class="card-grid">${s.servers.map((sr) => ServerCard({ server: sr, meta: s.meta }))}</div>`
        : html`<${SectionEmpty} text=${T.empty.servers.text} />`}
    </${Section}>

    <${Section} title=${T.queue.today}>
      ${mon && mon.turns_24h
        ? html`<div class="stat-grid">
            <${Stat} label=${T.queue.turns} value=${String(mon.turns_24h.turns)} />
            <${Stat} label=${T.queue.ok} value=${String(mon.turns_24h.ok)} />
            <${Stat} label=${T.queue.error} value=${String(mon.turns_24h.error)} />
            <${Stat} label=${T.queue.duration} value=${fmtDur(mon.turns_24h.avg_duration_sec)} />
          </div>`
        : html`<${SectionEmpty} text=${T.empty.monitoring.text} />`}
    </${Section}>

    <${Section} title=${T.queue.work} count=${running.length}>
      ${shownRunning.length
        ? html`<div class="srow-list">
            ${shownRunning.map((x) => SessionRow({ s: x, meta: s.meta }))}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyWork} />`}
    </${Section}>

    <${Section} title=${T.queue.queued} count=${queueRows.length}
      action=${queueRows.length ? html`<span class="section-action">
        <${Button} variant="secondary" label=${T.queue.clear} onClick=${clearQueue} />
      </span>` : null}>
      ${queueRows.length
        ? html`<div class="srow-list">
            ${queueRows.slice(0, UI.queueWorkMax).map((row) => {
              const why = whyText(s.meta, 'ineligible', row.why);
              const cTone = stateColorVar(String(row.class).toUpperCase());
              return html`<div class="srow qrow">
                <span class="qrow-pos">${row.rank}</span>
                <div class="srow-main">
                  <span class="srow-name" title=${row.sid}>${row.session}</span>
                  ${row.host ? html`<span class="srow-sub">${row.host}</span>` : null}
                  ${why ? html`<span class="srow-why">${why}</span>` : null}
                </div>
                <div class="srow-side">
                  <${Badge} tone=${cTone} label=${classLabel(s.meta, row.class)} />
                  <span class="qrow-wait">${row.wait_sec} ${T.units.sec}</span>
                </div>
              </div>`;
            })}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyQueued} />`}
    </${Section}>

    <${Section} title=${T.queue.attention} count=${hold.length}>
      ${hold.length
        ? html`<div class="srow-list">
            ${hold.map((x) => SessionRow({ s: x, meta: s.meta,
              why: whyText(s.meta, 'hold', x.hold_reason) }))}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyAttention} />`}
    </${Section}>

    ${externals.length
      ? html`<${Section} title=${T.externals.title} count=${externals.length}>
          <div class="srow-list">
            ${externals.map((e) => html`
              <div class="srow ext-row">
                <div class="srow-main">
                  <span class="srow-name mono" title=${e.exe + ' ' + (e.flags || '')}>${e.exe} ${e.flags || ''}</span>
                  <span class="srow-sub">${e.host} · pid ${e.pid} · ${T.externals.source}: ${e.source}</span>
                  <span class="srow-why">${T.externals.target}: <span class="mono">${e.target}</span></span>
                </div>
                <div class="srow-side">
                  <${Badge} tone=${EXT_TONE[e.status] || 'var(--color-text-muted)'}
                    label=${T.externals[EXT_STATUS[e.status]] || e.status} />
                  ${e.status === 'ACTIVE' ? html`
                    <${Button} variant="danger" label=${T.externals.kill}
                      onClick=${() => externalKill(e)} />
                    <${Button} variant="secondary" label=${T.externals.ignore}
                      onClick=${() => externalIgnore(e)} />` : null}
                </div>
              </div>`)}
          </div>
          <p class="ext-hint">${T.externals.hint}</p>
        </${Section}>`
      : null}
  </div>`;
}

// W7 (8.2, J9/J11): «Завершить» — узел сверяет uid+start_time и шлёт TERM.
function externalKill(e) {
  post('/api/v1/externals/kill', { host: e.host, pid: e.pid, start_time: e.start_time })
    .then((r) => {
      if (r.ok) actions.addToast({ kind: 'info', text: T.externals.killed });
      else actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
}
// W7 (8.2, J9/J11): «Игнорировать» — правило (host+exe+flags) в БД.
function externalIgnore(e) {
  post('/api/v1/externals/ignore', { host: e.host, exe: e.exe, flags: e.flags || '' })
    .then((r) => {
      if (r.ok) actions.addToast({ kind: 'info', text: T.externals.ignored });
      else actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
}
