// Экран «Очередь» (11.2): карточки серверов, «В работе», «Очередь» (с WHY),
// «Требуют внимания» (HOLD + why.text) и сводка «Сегодня». Живое — из store
// (SSE); строки очереди и сводка дня — разовый GET при входе на экран.
import { html } from '../html.js';
import { useState, useEffect, useRef } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post } from '../api.js';
import { T } from '../i18n/ru.js';
import { whyText, fmtDur } from '../labels.js';
import { UI, LAUNCH, FMT, W6 } from '../constants.js';
import { ServerCard } from '../components/server-card.js';
import { SessionRow } from '../components/session-row.js';
import { EmptyState } from '../components/empty-state.js';
import { Section, SectionEmpty } from '../components/section.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';
import { IconButton } from '../components/icon-button.js';

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
  // API-клиенты: разовый GET при входе на экран (список + учёт за сутки).
  const [apiClients, setApiClients] = useState([]);
  // Раздел постановки в очереди: остановка (Esc) — сверка реальной остановки
  // по активности (состояние сессии), не по факту отправки. sid → 'stopping'|…
  const [stopping, setStopping] = useState({});
  const sessionsRef = useRef(s.sessions);
  const queueRowsRef = useRef(queueRows);

  const reloadApiClients = () =>
    get('/api/v1/api-clients').then((r) => { if (r.ok && r.data) setApiClients(r.data); });

  // API-клиенты — разовый GET при входе на экран.
  useEffect(() => {
    reloadApiClients();
  }, []);
  // Строки очереди — live: пересъёмка /queue при каждом изменении среза
  // сессий (ресинк по SESSION_STATE), а не разовый снапшот. Без этого строка
  // удерживающей сессии (held) застревала в состоянии на момент входа на
  // экран → сессия «висела» в «Очередь» и «Свободных» разом.
  // Внешние кодеры — из store (s.externals), обновляются по SSE EXTERNALS.
  useEffect(() => {
    get('/api/v1/queue').then((r) => { if (r.ok && r.data) setQueueRows(r.data); });
  }, [s.sessions]);
  useEffect(() => { sessionsRef.current = s.sessions; }, [s.sessions]);
  useEffect(() => { queueRowsRef.current = queueRows; }, [queueRows]);

  // stopSession — «Остановить»: отправить Esc, затем сверять по активности —
  // сессия покинула RUNNING/DISPATCHING → остановлено; дедлайн → не остановилась.
  function stopSession(sid) {
    if (stopping[sid]) return;
    setStopping((m) => ({ ...m, [sid]: 'stopping' }));
    post(`/api/v1/sessions/${encodeURIComponent(sid)}/stop`, {}).catch(() => null).then((r) => {
      if (!r || !r.ok) {
        setStopping((m) => ({ ...m, [sid]: 'failed' }));
        actions.addToast({ kind: 'error', text: (r && r.data && r.data.detail) || T.session.stopErr });
        return;
      }
      actions.addToast({ kind: 'info', text: T.session.stopSent });
      const deadline = Date.now() + LAUNCH.stopDeadlineMs;
      const h = setInterval(() => {
        const sess = (sessionsRef.current || []).find((x) => x.sid === sid);
        const st = sess ? sess.state : '';
        const done = st && !['RUNNING', 'DISPATCHING'].includes(st);
        if (done) {
          setStopping((m) => ({ ...m, [sid]: 'done' }));
          actions.addToast({ kind: 'success', text: T.session.stopDone });
          clearInterval(h);
        } else if (Date.now() > deadline) {
          setStopping((m) => ({ ...m, [sid]: 'failed' }));
          actions.addToast({ kind: 'error', text: T.session.stopFailed });
          clearInterval(h);
        }
      }, LAUNCH.stopPollMs);
    });
  }

  // withdrawRow — «Отозвать» (на строке очереди): снять с очереди (dequeue).
  function withdrawRow(sid) {
    post(`/api/v1/sessions/${encodeURIComponent(sid)}/dequeue`, {}).catch(() => null).then((r) => {
      if (r && r.ok) {
        actions.addToast({ kind: 'info', text: T.session.withdrawDone });
        // переснять строки очереди + состояние (сессия ушла из очереди → IDLE).
        get('/api/v1/queue').then((q) => { if (q.ok && q.data) setQueueRows(q.data); });
        window.__runpilotResync && window.__runpilotResync();
      } else {
        actions.addToast({ kind: 'error', text: (r && r.data && r.data.detail) || T.session.withdrawErr });
      }
    });
  }

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }

  // item 6: панель в PROMPT («кодер ждёт разрешения») — «требует внимания».
  // View-level: session state при PROMPT остаётся RUNNING (автомата не
  // трогаем), поэтому выносим такие сессии из «В работе» в «Требуют внимания».
  const isPrompt = (x) => { const p = s.panes[x.sid]; return !!p && p.state === 'PROMPT'; };
  // «В работе» — RUNNING/DISPATCHING с действующей арендой (server). Все
  // пути снятия аренды (turn-done, server down, node lost, pane gone) снимают
  // аренду ДО смены state RUNNING→IDLE: в этом окне state ещё RUNNING, но
  // аренды нет (handleState отдаёт сессию без server) — карточка сервера её
  // не показывает, а фильтр по state показывал → «В работе» превышало
  // ёмкость (3 при 2 слотах). Фильтр по server закрывает окно.
  const running = s.sessions.filter((x) => (x.state === 'RUNNING' || x.state === 'DISPATCHING') && !isPrompt(x) && !!x.server);
  const hold = s.sessions.filter((x) => x.state === 'HOLD' || isPrompt(x));
  // Свободные — IDLE без удержания: удерживающая (held) сессия уже в
  // «Очередь», дублировать её в «Свободных» нельзя (сессия в двух статусах).
  const idleSessions = s.sessions.filter((x) => x.state === 'IDLE' && !x.held_since);
  const hasAny = s.servers.length > 0 || s.sessions.length > 0;

  if (!hasAny) {
    return html`<div class="page">
      <${EmptyState} icon="list-todo" title=${T.empty.queue.title} text=${T.empty.queue.text}
        actionLabel=${T.empty.queue.action} onAction=${() => actions.openDialog('new-session')} />
    </div>`;
  }

  const shownRunning = running.slice(0, UI.queueWorkMax);

  // «Очередь» — снапшот (разовый GET). Показываем строки, чья live-сессия
  // ещё в QUEUED (команда для запуска) ИЛИ удерживается (held_request=true):
  // удержание — сессия из терминала, слота нет, состояние IDLE; до её выдачи
  // аренды (RUNNING) она нигде else не видна. Ушедшие в запуск
  // (RUNNING/DISPATCHING) уже в «В работе», снятые (IDLE без held) и закрытые
  // (GONE) исключаем — иначе дублируем при переходе.
  const queuedRows = queueRows.filter((row) => {
    const x = s.sessions.find((y) => y.sid === row.sid);
    if (!x) return false;
    // Held — это IDLE (ждёт слот). RUNNING/DISPATCHING — в «В работе», в
    // «Очередь» не попадают, даже если в снапшоте ещё row.held (окно между
    // выдачей неявной аренды и снятием held_request).
    return x.state === 'QUEUED' || (x.state === 'IDLE' && !!x.held_since);
  });

  return html`<div class="page">
    <${Section} title=${T.queue.servers} count=${s.servers.length}>
      ${s.servers.length
        ? html`<div class="card-grid">${s.servers.map((sr) => ServerCard({ server: sr, meta: s.meta }))}</div>`
        : html`<${SectionEmpty} text=${T.empty.servers.text} />`}
    </${Section}>

    <${Section} title=${T.queue.work} count=${running.length}>
      ${shownRunning.length
        ? html`<div class="srow-list">
            ${shownRunning.map((x) => SessionRow({ s: x, meta: s.meta, actions: [
              {
                icon: 'x',
                title: stopping[x.sid] === 'stopping' ? T.session.stopping
                  : stopping[x.sid] === 'done' ? T.session.stopDone
                  : stopping[x.sid] === 'failed' ? T.session.stopFailed
                  : T.session.stop,
                variant: 'danger',
                loading: stopping[x.sid] === 'stopping',
                onClick: () => stopSession(x.sid),
              },
            ] }))}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyWork} />`}
    </${Section}>

    <${Section} title=${T.queue.queued} count=${queuedRows.length}
      action=${queuedRows.length ? html`<span class="section-action">
        <${Button} variant="secondary" label=${T.queue.clear} onClick=${clearQueue} />
      </span>` : null}>
      ${queuedRows.length
        ? html`<div class="srow-list">
            ${queuedRows.slice(0, UI.queueWorkMax).map((row) => {
              const x = s.sessions.find((y) => y.sid === row.sid);
              const held = !!x && !!x.held_since;
              const why = whyText(s.meta, 'ineligible', row.why);
              return html`<div class="srow qrow" onClick=${() => navigate('sessions', row.sid)}>
                <div class="srow-main">
                  <span class="srow-name" title=${row.sid}>${row.session}</span>
                  ${row.node_id ? html`<span class="srow-sub">${row.node_id}</span>` : null}
                  ${why ? html`<span class="srow-why">${why}</span>` : null}
                </div>
                <div class="srow-side">
                  ${held ? html`<${Badge} tone="var(--color-warning)" label=${T.queue.held} />` : null}
                  ${held && x.held_since ? html`<${HeldTimer} since=${x.held_since} />` : null}
                  <span class="qrow-wait">${row.wait_sec}${T.units.sec}</span>
                  <${IconButton} variant="secondary" icon="file-text" title=${T.session.editLaunch}
                    onClick=${(e) => { e.stopPropagation(); navigate('sessions', row.sid); }} />
                  <${IconButton} variant="danger" icon="x" title=${T.session.withdraw}
                    onClick=${(e) => { e.stopPropagation(); withdrawRow(row.sid); }} />
                </div>
              </div>`;
            })}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyQueued} />`}
    </${Section}>

    <${Section} title=${T.queue.attention} count=${hold.length}>
      ${hold.length
        ? html`<div class="srow-list">
            ${hold.map((x) => {
              const why = x.state === 'HOLD'
                ? whyText(s.meta, 'hold', x.hold_reason)
                : (s.panes[x.sid] && s.panes[x.sid].input_preview) || T.queue.promptWait;
              return SessionRow({ s: x, meta: s.meta, why });
            })}
          </div>`
        : html`<${SectionEmpty} text=${T.queue.emptyAttention} />`}
    </${Section}>

    ${idleSessions.length
      ? html`<${Section} title=${T.queue.free} count=${idleSessions.length}>
          <div class="srow-list">
            ${idleSessions.map((x) => SessionRow({ s: x, meta: s.meta }))}
          </div>
        </${Section}>`
      : null}

    <${ApiClientsSection} clients=${apiClients} onChanged=${reloadApiClients} />

    ${s.externals.length
      ? html`<${Section} title=${T.externals.title} count=${s.externals.length}>
          <div class="srow-list">
            ${s.externals.map((e) => ExternalRow({ e }))}
          </div>
          <p class="ext-hint">${T.externals.hint}</p>
        </${Section}>`
      : null}
  </div>`;
}

// API-клиенты (безсессионные OpenAI-запросы в общую очередь): список + учёт
// за сутки, создать/отключить/перевыпустить ключ. Ключ показывается один раз.
function ApiClientsSection({ clients, onChanged }) {
  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [newKey, setNewKey] = useState('');

  function create() {
    if (!name.trim()) return;
    post('/api/v1/api-clients', { name: name.trim() }).then((r) => {
      if (r.ok && r.data) {
        setNewKey(r.data.key || '');
        actions.addToast({ kind: 'success', text: T.queue.apiCreated });
        setName('');
        onChanged();
      } else {
        actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
      }
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  }
  function setEnabled(c, on) {
    post(`/api/v1/api-clients/${encodeURIComponent(c.name)}/${on ? 'enable' : 'disable'}`, {}).then((r) => {
      if (r.ok) {
        actions.addToast({ kind: 'info', text: on ? T.queue.apiEnabledDone : T.queue.apiDisabledDone });
        onChanged();
      } else {
        actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
      }
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  }
  function reissue(c) {
    post(`/api/v1/api-clients/${encodeURIComponent(c.name)}/reissue`, {}).then((r) => {
      if (r.ok && r.data) {
        setNewKey(r.data.key || '');
        actions.addToast({ kind: 'success', text: T.queue.apiReissued });
      } else {
        actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
      }
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  }

  return html`<${Section} title=${T.queue.apiClients} count=${clients.length}
    action=${html`<span class="section-action">
      <${Button} variant="secondary" label=${showCreate ? T.queue.apiCancel : T.queue.apiCreate}
        onClick=${() => setShowCreate((v) => !v)} />
    </span>`}>
    ${showCreate ? html`<div class="api-create">
      <input class="field" type="text" placeholder=${T.queue.apiName}
        value=${name} onInput=${(e) => setName(e.target.value)}
        onKeyDown=${(e) => { if (e.key === 'Enter') create(); }} />
      <${Button} variant="primary" label=${T.queue.apiCreate} onClick=${create} />
    </div>` : null}
    ${newKey ? html`<div class="api-key"><span>${T.queue.apiKey}</span>
      <code class="mono">${newKey}</code></div>` : null}
    ${clients.length
      ? html`<div class="srow-list">
          ${clients.map((c) => ApiClientRow({ c, onEnable: () => setEnabled(c, true),
            onDisable: () => setEnabled(c, false), onReissue: () => reissue(c) }))}
        </div>`
      : html`<${SectionEmpty} text=${T.queue.apiEmpty} />`}
  </${Section}>`;
}

function ApiClientRow({ c, onEnable, onDisable, onReissue }) {
  const tokens = c.prompt_tokens_24h + c.completion_tokens_24h;
  return html`<div class="srow">
    <div class="srow-main">
      <span class="srow-name mono">${c.name}</span>
      <span class="srow-sub">class ${c.class} · ${c.enabled ? T.queue.apiEnabled : T.queue.apiDisabled}</span>
      <span class="srow-why">${c.requests_24h} ${T.queue.apiRequests} · ${tokens} ${T.queue.apiTokens}</span>
    </div>
    <div class="srow-side">
      <${Badge} tone=${c.enabled ? 'var(--color-success)' : 'var(--color-text-muted)'}
        label=${c.enabled ? T.queue.apiEnabled : T.queue.apiDisabled} />
      <${Button} variant="secondary" label=${T.queue.apiReissue} onClick=${onReissue} />
      ${c.enabled
        ? html`<${Button} variant="danger" label=${T.queue.apiDisable} onClick=${onDisable} />`
        : html`<${Button} variant="secondary" label=${T.queue.apiEnable} onClick=${onEnable} />`}
    </div>
  </div>`;
}

// п.6: таймер удержания — now − held_since, обновляется раз в секунду.
function HeldTimer({ since }) {
  const [, force] = useState(0);
  useEffect(() => {
    const h = setInterval(() => force((x) => x + 1), FMT.msPerSec);
    return () => clearInterval(h);
  }, []);
  const start = new Date(since).getTime();
  const sec = start && !Number.isNaN(start)
    ? Math.max(0, Math.floor((Date.now() - start) / FMT.msPerSec))
    : null;
  return html`<span class="qrow-held" title=${T.queue.heldWait}>${fmtDur(sec)}</span>`;
}

// п.3: «кодер вне очереди» (target=unknown — без OPENAI_BASE_URL) — бейдж
// UNMANAGED + команда перезапуска под runpilot (runpilot run --dir <cwd>).
function ExternalRow({ e }) {
  const [copied, setCopied] = useState(false);
  const unmanaged = e.target === 'unknown';
  const dir = e.cwd || '~';
  const cmd = 'runpilot run --dir ' + dir;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(cmd);
      setCopied(true);
      setTimeout(() => setCopied(false), W6.copyFlashMs);
    } catch { /* clipboard недоступен (не-secure context) */ }
  };
  return html`
    <div class="srow ext-row">
      <div class="srow-main">
        <span class="srow-name mono" title=${e.exe + ' ' + (e.flags || '')}>${e.exe} ${e.flags || ''}</span>
        <span class="srow-sub">${e.host} · pid ${e.pid} · ${T.externals.source}: ${e.source}</span>
        <span class="srow-why">${T.externals.target}: <span class="mono">${e.target}</span></span>
        ${unmanaged ? html`
          <div class="ext-restart">
            <span class="ext-restart-label">${T.externals.restart}</span>
            <code class="mono ext-restart-cmd">${cmd}</code>
            <${Button} variant="text" label=${copied ? T.externals.copied : T.externals.copy}
              onClick=${copy} />
          </div>` : null}
      </div>
      <div class="srow-side">
        ${unmanaged ? html`<${Badge} tone="var(--color-warning)" label=${T.externals.unmanaged} />` : null}
        <${Badge} tone=${EXT_TONE[e.status] || 'var(--color-text-muted)'}
          label=${T.externals[EXT_STATUS[e.status]] || e.status} />
        <${Button} variant="danger" label=${T.externals.kill}
          onClick=${() => externalKill(e)} />
        ${e.status === 'ACTIVE' ? html`
          <${Button} variant="secondary" label=${T.externals.ignore}
            onClick=${() => externalIgnore(e)} />` : null}
      </div>
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
// W7 (8.2, J9/J11): «Игнорировать» — правило (host+exe+flags) в БД; на строке
// недоступного узла (pid передан) — удаляет строку.
function externalIgnore(e) {
  post('/api/v1/externals/ignore', { host: e.host, exe: e.exe, flags: e.flags || '', pid: e.pid, start_time: e.start_time })
    .then((r) => {
      if (r.ok) actions.addToast({ kind: 'info', text: T.externals.ignored });
      else actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || r.status });
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }));
}
