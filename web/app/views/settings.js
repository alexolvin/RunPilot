// Настройки (W6, 11.10): формы по схеме + ревизии (откат, J8) +
// импорт/экспорт + о системе. Бэкенд — W2 (GET/PATCH, schema, revisions,
// revert, export, import).
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post, patch, getCSRF } from '../api.js';
import { T } from '../i18n/ru.js';
import { W6 } from '../constants.js';
import { Button } from '../components/button.js';
import { Section, SectionEmpty } from '../components/section.js';

const TABS = ['form', 'revisions', 'io', 'about'];

// value ↔ строка для инпута.
function valToString(f, v) {
  if (v == null) return '';
  if (f.type === 'bool') return v ? 'true' : 'false';
  if (f.type === 'list') return Array.isArray(v) ? v.join(',') : String(v);
  return String(v);
}
function stringToVal(f, s) {
  if (f.type === 'int') return Number(s);
  if (f.type === 'bool') return s === 'true';
  if (f.type === 'list') return s.split(',').map((x) => x.trim()).filter(Boolean);
  return s;
}

export function SettingsPage() {
  const s = useStore();
  const [schema, setSchema] = useState([]);
  const [current, setCurrent] = useState(null); // {path: value}
  const [values, setValues] = useState({});    // {path: string}
  const [tab, setTab] = useState('form');
  const [query, setQuery] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');

  useEffect(() => {
    Promise.all([get('/api/v1/settings/schema'), get('/api/v1/settings')])
      .then(([sc, cur]) => {
        if (sc.ok && Array.isArray(sc.data)) setSchema(sc.data);
        if (cur.ok && cur.data) {
          setCurrent(cur.data);
          const v = {};
          for (const f of (sc.data || [])) v[f.path] = valToString(f, cur.data[f.path]);
          setValues(v);
        }
      });
  }, []);

  if (!s.loaded || !current) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }

  const dirty = schema.filter((f) => values[f.path] != null
    && values[f.path] !== valToString(f, current[f.path]));
  const setVal = (path) => (e) => { setMsg(''); setValues({ ...values, [path]: e.target.value }); };

  const save = () => {
    const changes = {};
    for (const f of dirty) changes[f.path] = stringToVal(f, values[f.path]);
    if (!Object.keys(changes).length) return;
    setBusy(true); setMsg('');
    patch('/api/v1/settings', changes)
      .then((r) => {
        if (r.ok && r.data) {
          setCurrent(r.data);
          const nv = {};
          for (const f of schema) nv[f.path] = valToString(f, r.data[f.path]);
          setValues(nv);
          setMsg(T.settings.saved);
          actions.addToast({ kind: 'info', text: T.settings.saved });
        } else {
          const probs = r.data && r.data.problems;
          setMsg(probs && probs.length ? probs.map((p) => p.path + ': ' + p.message).join('; ')
            : (r.data && r.data.detail) || 'error');
        }
      })
      .catch(() => setMsg('network'))
      .finally(() => setBusy(false));
  };

  return html`<div class="page">
    <div class="tabs">
      ${TABS.map((t) => html`<button class="tab-btn${t === tab ? ' is-active' : ''}"
        type="button" onClick=${() => setTab(t)}>${T.settings.tabs[t]}</button>`)}
    </div>
    ${tab === 'form' ? html`<${SettingsForm} schema=${schema} values=${values} setVal=${setVal}
      dirty=${dirty} onSave=${save} busy=${busy} msg=${msg} query=${query} setQuery=${setQuery} />` : null}
    ${tab === 'revisions' ? html`<${RevisionsTab} onReverted=${save} />` : null}
    ${tab === 'io' ? html`<${ImportExportTab} />` : null}
    ${tab === 'about' ? html`<${AboutTab} />` : null}
  </div>`;
}

function SettingsForm(props) {
  const { schema, values, setVal, dirty, onSave, busy, msg, query, setQuery } = props;
  const q = query.toLowerCase();
  const fields = schema.filter((f) => !q || f.path.toLowerCase().includes(q)
    || (f.label || '').toLowerCase().includes(q));
  const groups = {};
  for (const f of fields) { (groups[f.group] = groups[f.group] || []).push(f); }
  const dirtySet = new Set(dirty.map((f) => f.path));

  const fieldInput = (f) => {
    if (f.type === 'bool') {
      return html`<input type="checkbox" checked=${values[f.path] === 'true'}
        aria-label=${f.label || f.path} onInput=${setVal(f.path)} />`;
    }
    const type = f.type === 'int' ? 'number' : 'text';
    return html`<input type=${type} value=${values[f.path] || ''}
      aria-label=${f.label || f.path}
      min=${f.min != null ? f.min : null} max=${f.max != null ? f.max : null}
      onInput=${setVal(f.path)} />`;
  };

  return html`
    <input class="settings-search" type="text" placeholder=${T.settings.search}
      aria-label=${T.settings.search} value=${query} onInput=${(e) => setQuery(e.target.value)} />
    ${fields.length ? Object.entries(groups).map(([g, fs]) => html`
      <div class="settings-group">
        <h3>${g}</h3>
        ${fs.map((f) => html`
          <div class="field" key=${f.path} style=${{ display: 'grid', gridTemplateColumns: '220px 1fr', gap: 'var(--space-3)', alignItems: 'center' }}>
            <label style=${{ margin: 0 }}>
              <div style=${{ fontWeight: 500 }}>${f.label}</div>
              <div class="field-err" style=${{ color: 'var(--color-text-muted)' }}>${f.path}${f.unit ? ' · ' + f.unit : ''}</div>
            </label>
            <div>
              ${fieldInput(f)}
              ${f.help ? html`<div class="field-err" style=${{ color: 'var(--color-text-muted)' }}>${f.help}</div>` : null}
              ${dirtySet.has(f.path) ? html`<span class="badge" style=${{ '--tone': 'var(--color-warning)' }}>●</span>` : null}
            </div>
          </div>`)}
      </div>`
    ) : html`<${SectionEmpty} text=${T.settings.noResults} />`}
    <div style=${{ display: 'flex', gap: 'var(--space-2)', marginTop: 'var(--space-4)' }}>
      <${Button} variant="primary" label=${T.settings.save} loading=${busy}
        disabled=${!dirty.length} onClick=${onSave} />
      ${dirty.length ? html`<span class="dialog-err">${T.settings.dirty}: ${dirty.length}</span>` : null}
      ${msg ? html`<p class="dialog-err">${msg}</p>` : null}
    </div>`;
}

function RevisionsTab() {
  const [revs, setRevs] = useState([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState('');
  const load = () => get('/api/v1/settings/revisions').then((r) => {
    if (r.ok && Array.isArray(r.data)) setRevs(r.data);
  });
  useEffect(() => { load(); }, []);

  const revert = (id) => {
    setBusy(true); setMsg('');
    post('/api/v1/settings/revert', { id })
      .then((r) => {
        if (r.ok) { setMsg(T.settings.saved); actions.addToast({ kind: 'info', text: T.settings.revert + ' #' + id }); load(); }
        else setMsg((r.data && r.data.detail) || 'error');
      })
      .catch(() => setMsg('network'))
      .finally(() => setBusy(false));
  };

  if (!revs.length) return html`<${SectionEmpty} text=${T.settings.revEmpty} />`;
  return html`
    ${msg ? html`<p class="dialog-ok">${msg}</p>` : null}
    <table class="rev-table">
      <thead><tr>
        <th>${T.settings.revWhen}</th><th>${T.settings.revWho}</th>
        <th>${T.settings.revKey}</th><th>${T.settings.revert}</th>
      </tr></thead>
      <tbody>
        ${revs.map((rv) => html`<tr key=${rv.id}>
          <td class="rev-val">${rv.ts} (#${rv.id})</td>
          <td class="rev-who">${rv.author} / ${rv.source}</td>
          <td class="rev-key" title=${rv.diff}>${rv.diff ? rv.diff.slice(0, W6.revPreviewLen) : ''}${rv.diff && rv.diff.length > W6.revPreviewLen ? '…' : ''}</td>
          <td class="rev-cell-actions">
            <${Button} variant="secondary" label=${T.settings.revert} disabled=${busy} onClick=${() => revert(rv.id)} />
          </td>
        </tr>`)
        }
      </tbody>
    </table>`;
}

function ImportExportTab() {
  const [exported, setExported] = useState('');
  const [importText, setImportText] = useState('');
  const [ioMsg, setIoMsg] = useState('');
  const [busy, setBusy] = useState(false);

  const doExport = () => fetch('/api/v1/settings/export').then(async (r) => {
    if (r.ok) setExported(await r.text());
  });
  const doImport = (dry) => {
    setBusy(true); setIoMsg('');
    fetch('/api/v1/settings/import' + (dry ? '?dry_run=true' : ''), {
      method: 'POST', headers: { 'Content-Type': 'application/yaml', ...csrfHdr() },
      body: importText,
    }).then(async (r) => {
      const d = await r.json().catch(() => ({}));
      if (r.ok) setIoMsg(T.settings.importOk + (dry ? ' (' + T.settings.importDry + ')' : ''));
      else setIoMsg(T.settings.importErr + ': ' + (d.detail || d.code || r.status));
    }).catch(() => setIoMsg('network')).finally(() => setBusy(false));
  };

  return html`
    <${Section} title=${T.settings.export}>
      <${Button} variant="secondary" label=${T.settings.export} onClick=${doExport} />
      ${exported ? html`<pre class="code-block" style=${{ maxHeight: '300px', overflow: 'auto' }}>${exported}</pre>` : null}
    </${Section}>
    <${Section} title=${T.settings.import}>
      <textarea class="composer-input" style=${{ minHeight: '120px', fontFamily: 'var(--font-mono)' }}
        placeholder=${T.settings.importPh} value=${importText}
        onInput=${(e) => setImportText(e.target.value)} />
      <div style=${{ display: 'flex', gap: 'var(--space-2)', marginTop: 'var(--space-2)' }}>
        <${Button} variant="secondary" label=${T.settings.importDry} loading=${busy} disabled=${!importText} onClick=${() => doImport(true)} />
        <${Button} variant="primary" label=${T.settings.importApply} loading=${busy} disabled=${!importText} onClick=${() => doImport(false)} />
      </div>
    </${Section}>
    ${ioMsg ? html`<p class="dialog-ok">${ioMsg}</p>` : null}`;
}

function AboutTab() {
  const s = useStore();
  return html`
    <${Section} title=${T.settings.about}>
      <div class="sc-dl">
        <div><dt>${T.settings.aboutVersion}</dt><dd>${s.session.version || T.units.none}</dd></div>
        <div><dt>${T.settings.aboutMode}</dt><dd>${T.mode[s.mode] || s.mode}</dd></div>
      </div>
    </${Section}>
    <p class="dialog-note">${T.settings.text}</p>`;
}

// csrfHdr — заголовок CSRF для изменяющих fetch (обход обёртки api.js:
// импорт шлёт сырой YAML, а не JSON).
function csrfHdr() {
  const t = getCSRF();
  return t ? { 'X-RUNPILOT-CSRF': t } : {};
}
