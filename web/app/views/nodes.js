// Экран «Узлы» (11.7): подключённые узлы + детальная (/nodes/<host>).
// W6 (14/5.3, J7): подключение узла (одна команда, enroll) и удаление в обоих
// режимах (панели становятся UNMANAGED). Данные — store (SSE).
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { post, del } from '../api.js';
import { T } from '../i18n/ru.js';
import { HTTP, FMT } from '../constants.js';
import { sessionStateLabel, whyText } from '../labels.js';
import { EmptyState } from '../components/empty-state.js';
import { SessionRow } from '../components/session-row.js';
import { Button } from '../components/button.js';
import { Dialog } from '../components/dialog.js';
import { Section, SectionEmpty } from '../components/section.js';
import { Badge } from '../components/badge.js';
import { navigate } from '../router.js';

const ORDER = ['HOLD', 'RUNNING', 'DISPATCHING', 'QUEUED', 'DETACHED', 'IDLE', 'GONE'];

// N9/N10 (14.4): здоровье узла — проблемы (no_tmux/no_qwen/disk_low) и
// расхождение часов. При проблемах «Новая сессия»/«Перезапустить» неактивны.
const PROBLEM_LABEL = { no_tmux: 'hNoTmux', no_qwen: 'hNoQwen', disk_low: 'hDiskLow' };
function skewText(ms) {
  if (!ms) return T.units.none;
  const a = Math.abs(ms);
  if (a < FMT.msPerSec) return (ms < 0 ? '−' : '') + a + ' ' + T.units.ms;
  return (ms < 0 ? '−' : '') + (a / FMT.msPerSec).toFixed(1) + ' ' + T.units.sec;
}
function NodeHealth({ n }) {
  const h = n.health;
  if (!h) return null;
  const problems = h.problems.length
    ? h.problems.map((p) => T.nodes[PROBLEM_LABEL[p]] || p)
    : [T.nodes.hOk];
  const hasProblems = h.problems.length > 0;
  return html`<${Section} title=${T.nodes.health}>
    <div class="node-health">
      ${problems.map((p, i) => html`
        <${Badge} key=${p}
          tone=${i === 0 && !hasProblems ? 'var(--color-success)' : 'var(--color-warning)'}
          label=${p} />`)}
      <span class="node-health-skew">
        ${T.nodes.hTimeSkew}: ${skewText(h.time_skew_ms)}
      </span>
      ${h.disk_free_mb ? html`<span class="node-health-disk">
        ${T.nodes.hDisk}: ${h.disk_free_mb} ${T.units.mb}
      </span>` : null}
      ${h.qwen_version ? html`<span class="node-health-qwen">
        ${T.nodes.hQwenVer}: ${h.qwen_version}
      </span>` : null}
      ${hasProblems ? html`<p class="node-health-warn">
        ${T.nodes.hNoNew}${problems.join(', ')}
      </p>` : null}
    </div>
  </${Section}>`;
}

function NodeCard(props) {
  const n = props.n;
  return html`<div class="node-card card" role="link"
    onClick=${() => navigate('nodes', n.node_id)}>
    <div class="node-card-head">
      <span class="server-dot" style=${{ '--tone': 'var(--color-success)' }}></span>
      <span class="server-card-name">${n.node_id}</span>
      <span class="node-card-count">${props.count} ${T.nodes.sessions}${n.unmanaged ? ' · ' + n.unmanaged + ' ' + T.nodes.unmanaged : ''}</span>
    </div>
    <div class="node-card-sub">${n.host_ip || T.units.none} · ${n.runpilot_version || T.units.none}</div>
    ${n.version_mismatch ? html`<div class="node-card-mismatch">
      <${Badge} tone="var(--color-warning)" label=${T.nodes.versionMismatch} /></div>` : null}
    ${n.sockets.length ? html`<div class="node-card-socks">${n.sockets.join(', ')}</div>` : null}
  </div>`;
}

export function NodesPage() {
  const s = useStore();
  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }
  if (s.pageParam) return NodeDetail({ host: s.pageParam });
  return html`<div class="page">
    ${s.nodes.length
      ? html`<div class="card-grid">${s.nodes.map((n) => NodeCard({
          n, count: s.sessions.filter((x) => x.node_id === n.node_id).length }))}</div>`
      : html`<${EmptyState} icon="monitor" title=${T.empty.nodes.title} text=${T.empty.nodes.text}
          actionLabel=${T.empty.nodes.action} onAction=${() => actions.openDialog('enroll-node')} />`}
  </div>`;
}

function NodeDetail({ host }) {
  const s = useStore();
  const [del, setDel] = useState(false);
  // host = идентичность узла (host:os_user).
  const node = s.nodes.find((x) => x.node_id === host);
  const list = s.sessions.filter((x) => x.node_id === host);
  const groups = ORDER
    .map((st) => ({ st, list: list.filter((x) => x.state === st) }))
    .filter((g) => g.list.length > 0);
  return html`<div class="page">
    <${Button} variant="text" label=${T.nodes.host + ': ' + host} onClick=${() => navigate('nodes')} />
    ${node ? NodeHealth({ n: node }) : null}
    ${node && node.unmanaged ? html`<${SectionEmpty} text=${node.unmanaged + ' ' + T.nodes.unmanaged} />` : null}
    ${groups.length
      ? groups.map((g) => html`<${Section} title=${sessionStateLabel(s.meta, g.st)}
          count=${g.list.length}>
          <div class="srow-list">
            ${g.list.map((x) => SessionRow({ s: x, meta: s.meta,
              why: g.st === 'HOLD' ? whyText(s.meta, 'hold', x.hold_reason) : '' }))}
          </div>
        </${Section}>`)
      : html`<${SectionEmpty} text=${T.empty.sessions.text} />`}
    <div style=${{ marginTop: 'var(--space-5)' }}>
      <${Button} variant="danger" label=${T.nodes.delTitle} onClick=${() => setDel(true)} />
    </div>
    ${del ? html`<${NodeDelete} host=${host} onClose=${() => setDel(false)} />` : null}
  </div>`;
}

// --- Подключение узла (14, CONTROL 1, J7): одна команда ---
export function NodeEnroll({ onClose }) {
  const s = useStore();
  const [user, setUser] = useState('');
  const [host, setHost] = useState('');
  const [password, setPassword] = useState('');
  const [ready, setReady] = useState(false);
  // Базовый набор узлов на момент открытия: новый узел (node_id) = «подключился».
  const base = useState(() => new Set(s.nodes.map((n) => n.node_id)))[0];
  // Результат установки (auto_install) — из store (SSE NODE_ENROLL).
  const enroll = s.enroll;

  // При открытии диалога сбрасываем состояние установки (не показывать чужой
  // результат прошлой попытки).
  useEffect(() => { actions.setEnroll(null); }, []);

  useEffect(() => {
    if (ready) return;
    const isNew = s.nodes.some((n) => !base.has(n.node_id));
    if (isNew) setReady(true);
  }, [s.nodes]);

  // Узел подключился (появился в s.nodes) → закрыть диалог: узел виден в списке
  // (ресинк по NODE_STATE), оператору не нужно вручную жать «Закрыть».
  useEffect(() => {
    if (ready) onClose();
  }, [ready]);

  // «Добавить» — автоустановка: координатор сам выполняет ssh, результат
  // приходит по SSE (NODE_ENROLL).
  const add = () => {
    const h = host.trim();
    if (!h) return;
    actions.setEnroll({ status: 'installing', host: h, output: '' });
    post('/api/v1/nodes/enroll', {
      user: user.trim() || undefined,
      host: h,
      password: password || undefined,
      auto_install: true,
    }).then((r) => {
      if (!(r.ok || r.status === HTTP.accepted)) {
        actions.setEnroll({ status: 'failed', host: h,
          output: (r.data && r.data.detail) || ('HTTP ' + r.status) });
      }
    }).catch(() => {
      actions.setEnroll({ status: 'failed', host: h, output: 'network' });
    });
  };

  const installing = !!(enroll && enroll.status === 'installing');
  const done = !!(enroll && (enroll.status === 'success' || enroll.status === 'failed'));

  return html`<${Dialog} title=${T.nodes.enTitle} onClose=${onClose}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${onClose} />
      ${!ready ? html`<${Button} variant="primary" label=${T.nodes.enAdd}
        loading=${installing} disabled=${!host.trim() || installing} onClick=${add} />` : null}`}>
    <p class="dialog-note">${T.nodes.enNote}</p>
    <div class="field" style=${{ marginBottom: 'var(--space-3)' }}>
      <label>${T.nodes.enHost}</label>
      <input type="text" value=${host} placeholder=${T.nodes.enHostPlaceholder}
        onInput=${(e) => setHost(e.target.value)} />
      <p class="dialog-note" style=${{ marginTop: 'var(--space-1)' }}>${T.nodes.enHostNote}</p>
    </div>
    <div class="field" style=${{ marginBottom: 'var(--space-3)' }}>
      <label>${T.nodes.enUser}</label>
      <input type="text" value=${user} placeholder=${T.nodes.enUserPlaceholder}
        onInput=${(e) => setUser(e.target.value)} />
      <p class="dialog-note" style=${{ marginTop: 'var(--space-1)' }}>${T.nodes.enUserNote}</p>
    </div>
    <div class="field" style=${{ marginBottom: 'var(--space-3)' }}>
      <label>${T.nodes.enPassword}</label>
      <input type="password" value=${password} placeholder=${T.nodes.enPasswordPlaceholder}
        onInput=${(e) => setPassword(e.target.value)} autoComplete="off" />
      <p class="dialog-note" style=${{ marginTop: 'var(--space-1)' }}>${T.nodes.enPasswordNote}</p>
    </div>
    ${installing ? html`<p class="dialog-note">${T.nodes.enInstalling}</p>` : null}
    ${done ? html`
      <p class=${enroll.status === 'success' ? 'dialog-ok' : 'dialog-err'}>
        ${enroll.status === 'success' ? T.nodes.enInstallOk : T.nodes.enInstallFail}
      </p>
      ${enroll.output ? html`<div class="code-block">${enroll.output}</div>` : null}` : null}
    <p class=${ready ? 'dialog-ok' : 'dialog-note'} style=${{ marginTop: 'var(--space-3)' }}>
      ${ready ? T.nodes.enConnected : T.nodes.enWaiting}
    </p>
  </${Dialog}>`;
}

// --- Удаление узла (5.3, J7): now / after_turns ---
function NodeDelete({ host, onClose }) {
  const [mode, setMode] = useState('now');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const doDelete = () => {
    setBusy(true); setErr('');
    del('/api/v1/nodes/' + encodeURIComponent(host) + '?mode=' + mode)
      .then((r) => {
        if (r.ok || r.status === HTTP.accepted) {
          onClose(); navigate('nodes');
          actions.addToast({ kind: 'info', text: host + ': ' + T.nodes.delTitle });
          return;
        }
        setErr((r.data && r.data.detail) || (r.data && r.data.code) || ('HTTP ' + r.status));
      })
      .catch(() => setErr('network'))
      .finally(() => setBusy(false));
  };

  return html`<${Dialog} title=${T.nodes.delTitle + ': ' + host} onClose=${onClose}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${onClose} />
      <${Button} variant="danger" label=${T.nodes.delConfirm} loading=${busy} onClick=${doDelete} />`}>
    <p class="dialog-note">${T.nodes.delNote}</p>
    <div class="check-row">
      <label>
        <input type="radio" name="node-mode" checked=${mode === 'now'}
          onChange=${() => setMode('now')} /> ${T.nodes.delNow}
      </label>
    </div>
    <div class="check-row">
      <label>
        <input type="radio" name="node-mode" checked=${mode === 'after_turns'}
          onChange=${() => setMode('after_turns')} /> ${T.nodes.delAfter}
      </label>
    </div>
    ${err ? html`<p class="dialog-err">${err}</p>` : null}
  </${Dialog}>`;
}
