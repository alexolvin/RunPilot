// Экран «Серверы» (11.6): сетка карточек + детальная (/servers/<name>) с
// метриками. W6 (5.1/5.2): мастер нового сервера (пробный запрос) и удаление
// в обоих режимах (J6). Данные — store (SSE); история — GET /monitoring.
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post, patch, del } from '../api.js';
import { T } from '../i18n/ru.js';
import { HTTP, W6 } from '../constants.js';
import { stateColorVar } from '../state-style.js';
import { serverStateLabel, fmtPct, fmtTokS, fmtW } from '../labels.js';
import { ServerCard } from '../components/server-card.js';
import { Sparkline } from '../components/sparkline.js';
import { EmptyState } from '../components/empty-state.js';
import { Stat } from '../components/stat.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';
import { Dialog } from '../components/dialog.js';
import { Section, SectionEmpty } from '../components/section.js';
import { navigate } from '../router.js';

export function ServersPage() {
  const s = useStore();
  const [wizard, setWizard] = useState(false);
  if (s.pageParam) return ServerDetail({ name: s.pageParam });
  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }
  return html`<div class="page">
    <div class="page-head-row">
      <h2 class="detail-title">${s.servers.length + ' ' + T.pages.servers.toLowerCase()}</h2>
      <${Button} variant="primary" label=${T.servers.add} onClick=${() => setWizard(true)} />
    </div>
    ${s.servers.length
      ? html`<div class="card-grid">${s.servers.map((sr) => ServerCard({ server: sr, meta: s.meta }))}</div>`
      : html`<${EmptyState} icon="server" title=${T.empty.servers.title} text=${T.empty.servers.text}
          actionLabel=${T.empty.servers.action} onAction=${() => setWizard(true)} />`}
    ${wizard ? html`<${ServerWizard} onClose=${() => setWizard(false)} />` : null}
  </div>`;
}

function ServerDetail({ name }) {
  const s = useStore();
  const [mon, setMon] = useState(null);
  const [del, setDel] = useState(false);
  const [drain, setDrain] = useState(false);
  const [edit, setEdit] = useState(false);
  useEffect(() => {
    get('/api/v1/monitoring').then((r) => { if (r.ok && r.data) setMon(r.data); });
  }, [name]);

  const server = s.servers.find((x) => x.name === name);
  const mserver = mon && mon.servers.find((x) => x.name === name);
  const tone = server ? stateColorVar(server.state) : 'var(--color-text-muted)';

  if (!server) {
    return html`<div class="page"><${SectionEmpty} text=${T.units.none} /></div>`;
  }
  const hist = (mserver && mserver.history) || [];
  // Период динамики — из /monitoring (window_min = monitor.history_window_min).
  const windowMin = mon && mon.window_min;
  const sparkTitle = (windowMin !== undefined && windowMin !== null && String(windowMin) !== '')
    ? T.servers.spark + ' · ' + T.servers.sparkWindow(windowMin)
    : T.servers.spark;
  return html`<div class="page">
    <div style=${{ alignSelf: 'flex-start' }}>
      <${Button} variant="text" label=${T.servers.back} onClick=${() => navigate('servers')} />
    </div>
    <div class="detail-head card">
      <span class="server-dot" style=${{ '--tone': tone }}></span>
      <h2 class="detail-title">${server.name}</h2>
      ${server.removing ? html`<${Badge} tone="var(--color-warning)" label=${T.servers.removing} />` : null}
      <${Badge} tone=${tone} label=${serverStateLabel(s.meta, server.state)} />
    </div>
    <div class="stat-grid">
      <${Stat} label=${T.servers.slots} value=${server.running + '/' + server.total} />
      <${Stat} label=${T.servers.kv} value=${server.missing ? T.units.none : fmtPct(server.kv_pct)} />
      <${Stat} label=${T.servers.gen} value=${server.missing ? T.units.none : fmtTokS(server.gen_tok_s)} />
      <${Stat} label=${T.servers.power}
        value=${server.power == null ? T.units.none : fmtW(server.power)} />
    </div>
    <${Section} title=${sparkTitle}>
      ${hist.length
        ? html`<div class="spark-block">
            <div class="spark-row">
              <span class="spark-k">${T.monitoring.gen}</span>
              <${Sparkline} values=${hist.map((p) => p.gen_tok_s)} tone=${tone} />
            </div>
            <div class="spark-row">
              <span class="spark-k">${T.monitoring.kv}</span>
              <${Sparkline} values=${hist.map((p) => p.kv)} tone=${tone} />
            </div>
          </div>`
        : html`<${SectionEmpty} text=${T.empty.monitoring.text} />`}
    </${Section}>
    <div style=${{ marginTop: 'var(--space-5)' }} class="detail-actions">
      <${Button} variant="secondary" label=${T.servers.edit} onClick=${() => setEdit(true)} />
      <${Button} variant="secondary" label=${T.servers.drain} onClick=${() => setDrain(true)} />
      <${Button} variant="secondary"
        label=${server.state === 'PAUSED' ? T.servers.resume : T.servers.pause}
        onClick=${() => togglePause(name, server.state)} />
      <${Button} variant="danger" label=${T.servers.delTitle} onClick=${() => setDel(true)} />
    </div>
    ${del ? html`<${ServerDelete} name=${name} used=${server.running} onClose=${() => setDel(false)} />` : null}
    ${drain ? html`<${ServerDrain} name=${name} onClose=${() => setDrain(false)} />` : null}
    ${edit ? html`<${ServerWizard} server=${server} onClose=${() => setEdit(false)} />` : null}
  </div>`;
}

// --- J4: снять ходы с сервера (drain) ---
function ServerDrain({ name, onClose }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const doDrain = () => {
    setBusy(true); setErr('');
    post('/api/v1/servers/' + encodeURIComponent(name) + '/drain', { on: true })
      .then((r) => {
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.servers.drained });
          onClose();
          return;
        }
        setErr((r.data && r.data.detail) || ('HTTP ' + r.status));
      })
      .catch(() => setErr('network'))
      .finally(() => setBusy(false));
  };
  return html`<${Dialog} title=${T.servers.drain + ': ' + name} onClose=${onClose}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${onClose} />
      <${Button} variant="primary" label=${T.servers.drain} loading=${busy} onClick=${doDrain} />`}>
    <p class="dialog-note">${T.servers.drainNote}</p>
    ${err ? html`<p class="dialog-err">${err}</p>` : null}
  </${Dialog}>`;
}

// --- Пауза: временно вывести сервер из выдачи (идущие ходы доделываются) ---
function togglePause(name, state) {
  const on = state !== 'PAUSED';
  post('/api/v1/servers/' + encodeURIComponent(name) + '/pause', { on })
    .then((r) => {
      if (r.ok) {
        actions.resync();
        actions.addToast({ kind: 'info', text: (on ? T.servers.pauseDone : T.servers.resumeDone) + ': ' + name });
        return;
      }
      actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || ('HTTP ' + r.status) });
    })
    .catch(() => actions.addToast({ kind: 'error', text: 'network' }));
}

// --- Мастер нового сервера (5.1, J6) / редактирование (5.1) ---
// server — передан (редактирование): prefill из GET /servers/<name>,
// сохранение — PATCH (имя не меняется — оно адрес сервера).
function serverBody(v) {
  return {
    Name: v.name, Priority: Number(v.priority) || W6.defaultPriority, Slots: Number(v.slots) || 1,
    Accept: v.accept.split(',').map((x) => x.trim()).filter(Boolean),
    HealthURL: v.health, MetricsURL: v.metrics,
    Upstreams: { OpenAI: { URL: v.upUrl, Model: v.model, KeyEnv: v.keyEnv } },
  };
}

function serverPatchBody(v) {
  return {
    priority: Number(v.priority), slots: Number(v.slots),
    health_url: v.health, metrics_url: v.metrics,
    accept: v.accept.split(',').map((x) => x.trim()).filter(Boolean),
    upstreams: { openai: { url: v.upUrl, model: v.model, key_env: v.keyEnv } },
  };
}

const EMPTY_WIZARD = {
  name: '', priority: '10', slots: '1', health: '', metrics: '',
  upUrl: '', model: '', keyEnv: '', accept: 'resume,high,normal,low',
};

function wizardFromConfig(c) {
  const o = (c.Upstreams && c.Upstreams.OpenAI) || {};
  return {
    name: c.Name || '', priority: String(c.Priority != null ? c.Priority : '10'),
    slots: String(c.Slots || '1'), health: c.HealthURL || '', metrics: c.MetricsURL || '',
    upUrl: o.URL || '', model: o.Model || '', keyEnv: o.KeyEnv || '',
    accept: (c.Accept || []).join(', '),
  };
}

function ServerWizard({ onClose, server }) {
  const editing = !!server;
  const [v, setV] = useState(EMPTY_WIZARD);
  const [ready, setReady] = useState(!editing);
  const [busy, setBusy] = useState(false);
  const [probe, setProbe] = useState(null);
  const [err, setErr] = useState('');
  const set = (k) => (e) => setV((prev) => ({ ...prev, [k]: e.target.value }));

  // URL апстрима → авто-подстановка health/metrics (base + '/health'|'/metrics'),
  // пока в этих полях нет своего значения — вводить полные URL вручную не нужно.
  const setUpUrl = (e) => {
    const url = e.target.value;
    setV((prev) => {
      const base = url.replace(/\/+$/, '');
      return {
        ...prev,
        upUrl: url,
        health: prev.health !== '' ? prev.health : (base ? base + '/health' : ''),
        metrics: prev.metrics !== '' ? prev.metrics : (base ? base + '/metrics' : ''),
      };
    });
  };

  // Редактирование: конфигурация сервера (не живые метрики).
  useEffect(() => {
    if (!editing) return;
    get('/api/v1/servers/' + encodeURIComponent(server.name))
      .then((r) => {
        if (r.ok && r.data) { setV(wizardFromConfig(r.data)); setReady(true); }
        else setErr((r.data && r.data.detail) || 'HTTP ' + r.status);
      })
      .catch(() => setErr('network'));
  }, [editing, server]);

  const doProbe = () => {
    setBusy(true); setProbe(null); setErr('');
    post('/api/v1/servers/probe', serverBody(v))
      .then((r) => setProbe(r.ok && r.data ? r.data : null))
      .catch(() => setProbe(null))
      .finally(() => setBusy(false));
  };

  const save = () => {
    setBusy(true); setErr('');
    const req = editing
      ? patch('/api/v1/servers/' + encodeURIComponent(v.name), serverPatchBody(v))
      : post('/api/v1/servers', serverBody(v));
    req.then((r) => {
      if (r.ok) {
        actions.resync(); // создание/изменение сервера — обновляем срез
        onClose();
        actions.addToast({ kind: 'info',
          text: (editing ? T.servers.wEdit : T.servers.wizard) + ': ' + v.name });
      } else {
        setErr((r.data && r.data.detail) || (r.data && r.data.code) || ('HTTP ' + r.status));
      }
    })
      .catch(() => setErr('network'))
      .finally(() => setBusy(false));
  };

  // Проверка имеет смысл только с URL: пустой URL ≠ «сервер отвечает».
  const canProbe = v.upUrl !== '';
  const probeOk = probe && probe.health_ok && (probe.upstream_ok === undefined || probe.upstream_ok);
  const probeLabel = busy
    ? T.servers.wTesting
    : (probeOk ? T.servers.wTestOk : T.servers.wTestFail);

  return html`<${Dialog} title=${editing ? T.servers.wEdit + ': ' + v.name : T.servers.wizard}
    onClose=${onClose} wide=${true}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${onClose} />
      <${Button} variant="secondary" label=${probeLabel}
        loading=${busy && probe === null} disabled=${busy || !canProbe}
        title=${canProbe ? '' : T.servers.wTestNeedURL} onClick=${doProbe} />
      <${Button} variant="primary" label=${editing ? T.servers.wSaveEdit : T.servers.wSave}
        loading=${busy} disabled=${!ready || !v.name} onClick=${save} />`}>
    ${!ready
      ? html`<p class="dialog-note">${T.newSession.nodeOnline}</p>`
      : html`<div>
        <div class="field-row">
          <div class="field field--grow">
            <label>${T.servers.wName}*
              <input value=${v.name} placeholder=${T.servers.wNamePh}
                disabled=${editing ? true : null} onInput=${set('name')} />
            </label>
            <div class="field-err field-hint" title=${T.servers.wNameHint}>* ${T.servers.wNameHint}</div>
          </div>
          <div class="field field--narrow">
            <label>${T.servers.wSlots}
              <input type="number" min="1" value=${v.slots} onInput=${set('slots')} />
            </label>
          </div>
          <div class="field field--prio">
            <label>${T.servers.wPriority}
              <input type="number" value=${v.priority} onInput=${set('priority')} />
            </label>
          </div>
        </div>
        <h3 class="section-title" style=${{ margin: 'var(--space-4) 0 var(--space-2)' }}>${T.servers.wUpstream}</h3>
        <div class="field">
          <label>${T.servers.wUpUrl}<input value=${v.upUrl} placeholder=${T.servers.wUpUrlPh} onInput=${setUpUrl} /></label>
        </div>
        <div class="field-row">
          <div class="field">
            <label>${T.servers.wUpModel}<input value=${v.model} placeholder=${T.servers.wUpModelPh} onInput=${set('model')} /></label>
          </div>
          <div class="field">
            <label>${T.servers.wUpKeyEnv}<input value=${v.keyEnv} placeholder=${T.servers.wUpKeyEnvPh} onInput=${set('keyEnv')} /></label>
          </div>
        </div>
        <div class="field-row">
          <div class="field">
            <label>${T.servers.wHealth}<input value=${v.health} placeholder=${T.servers.wHealthPh} onInput=${set('health')} /></label>
          </div>
          <div class="field">
            <label>${T.servers.wMetrics}<input value=${v.metrics} placeholder=${T.servers.wMetricsPh} onInput=${set('metrics')} /></label>
          </div>
        </div>
        <div class="field">
          <label>${T.servers.wAccept}<input value=${v.accept} onInput=${set('accept')} /></label>
          <div class="field-err field-hint">${T.servers.wAcceptHint}</div>
        </div>
      </div>`}
    ${probe ? html`<p class=${probeOk ? 'dialog-ok' : 'dialog-err'}>
      ${probeOk ? T.servers.wTestOk : T.servers.wTestFail}:
      health=${probe.health_ok ? '✓' : '✗'}${probe.upstream_ok != null ? ' · upstream=' + (probe.upstream_ok ? '✓' : '✗') : ''}
    </p>` : null}
    ${err ? html`<p class="dialog-err">${err}</p>` : null}
  </${Dialog}>`;
}

// --- Удаление сервера (5.2, J6): now / after_turns ---
function ServerDelete({ name, used, onClose }) {
  const [mode, setMode] = useState(used > 0 ? 'after_turns' : 'now');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const doDelete = () => {
    setBusy(true); setErr('');
    del('/api/v1/servers/' + encodeURIComponent(name) + '?mode=' + mode)
      .then((r) => {
        if (r.ok || r.status === HTTP.accepted) {
          actions.resync(); // сервер удалён — обновляем список из /state
          onClose(); navigate('servers'); return;
        }
        setErr((r.data && r.data.detail) || (r.data && r.data.code) || ('HTTP ' + r.status));
      })
      .catch(() => setErr('network'))
      .finally(() => setBusy(false));
  };

  return html`<${Dialog} title=${T.servers.delTitle + ': ' + name} onClose=${onClose}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${onClose} />
      <${Button} variant="danger" label=${T.servers.delConfirm} loading=${busy} onClick=${doDelete} />`}>
    ${used > 0 ? html`<p class="dialog-note">${T.servers.delActive}</p>` : null}
    <div class="check-row">
      <label>
        <input type="radio" name="srv-mode" checked=${mode === 'now'}
          onChange=${() => setMode('now')} /> ${T.servers.delNow} — ${T.servers.delNowDesc}
      </label>
    </div>
    <div class="check-row">
      <label>
        <input type="radio" name="srv-mode" checked=${mode === 'after_turns'}
          onChange=${() => setMode('after_turns')} /> ${T.servers.delAfter} — ${T.servers.delAfterDesc}
      </label>
    </div>
    ${err ? html`<p class="dialog-err">${err}</p>` : null}
  </${Dialog}>`;
}
