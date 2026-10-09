// Новая сессия из веба (W6, 11.5/13.2; CONTROL 5/6): имя (name-check),
// узел, каталог (автодополнение из project_roots узла) → POST /sessions/spawn.
// Каталог валидирует узел: DIR_FORBIDDEN / DIR_NOT_FOUND → ошибка.
// W13 этап 7: предпроверка (коллизия model_alias) — на узле, при нарушении
// spawn возвращает 409 PRECHECK_FAILED с причиной.
// W13 этап 8: имя = последний компонент пути папки (пока оператор не правил);
// каталог — раскрывающийся список с поиском (фильтр project_dir_match).
import { html } from '../html.js';
import { useState, useEffect, useRef } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { get, post, qs } from '../api.js';
import { T } from '../i18n/ru.js';
import { W6 } from '../constants.js';
import { Dialog } from './dialog.js';
import { Button } from './button.js';

// baseName — последний компонент пути (без учёта символа /).
function baseName(p) {
  if (!p) return '';
  const parts = p.replace(/\/+$/, '').split('/');
  return parts[parts.length - 1] || '';
}

export function NewSessionDialog() {
  const s = useStore();
  const [name, setName] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const [host, setHost] = useState(s.nodes.length ? s.nodes[0].host : '');
  const [dir, setDir] = useState('');
  const [nameUsed, setNameUsed] = useState(false);
  const [dirs, setDirs] = useState([]);
  const [roots, setRoots] = useState([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [ok, setOk] = useState('');
  const dirRef = useRef(null);

  // Имя = последний компонент пути папки (пока оператор не правил).
  // Занято → -2, -3… (первое свободное).
  useEffect(() => {
    if (nameTouched || !dir) return;
    const base = baseName(dir);
    if (!base) return;
    let live = true;
    const tryName = (n) => {
      if (n.length < W6.nameCheckMinLen) { if (live) setName(n); return; }
      get('/api/v1/sessions/name-check' + qs({ name: n }))
        .then((r) => {
          if (!live) return;
          if (r.ok && r.data && r.data.used) {
            const m = n.match(/^(.*?)(-\d+)?$/);
            const stem = m ? m[1] : n;
            const num = m && m[W6.nameSuffixGroup] ? parseInt(m[W6.nameSuffixGroup].slice(1), W6.parseIntBase) : 1;
            tryName(stem + '-' + (num + 1));
          } else {
            setName(n);
          }
        });
    };
    tryName(base);
    return () => { live = false; };
  }, [dir, nameTouched]);

  // Имя занято? (name-check) — при вводе имени.
  useEffect(() => {
    if (name.length < W6.nameCheckMinLen) { setNameUsed(false); return; }
    let live = true;
    const h = setTimeout(() => {
      get('/api/v1/sessions/name-check' + qs({ name }))
        .then((r) => { if (live && r.ok && r.data) setNameUsed(!!r.data.used); });
    }, W6.nameCheckDebounceMs);
    return () => { live = false; clearTimeout(h); };
  }, [name]);

  // Каталог: автодополнение (подкаталоги project_roots узла) по префиксу;
  // roots — сами project_roots узла (подсказка с конкретными путями).
  useEffect(() => {
    if (!host) { setDirs([]); setRoots([]); return; }
    let live = true;
    const h = setTimeout(() => {
      get('/api/v1/nodes/' + encodeURIComponent(host) + '/dirs' + qs({ prefix: dir }))
        .then((r) => {
          if (!live || !r.ok || !r.data) return;
          setDirs(r.data.dirs || []);
          setRoots(r.data.roots || []);
        });
    }, W6.dirDebounceMs);
    return () => { live = false; clearTimeout(h); };
  }, [dir, host]);

  const close = () => actions.closeDialog();

  const create = () => {
    if (busy || !dir) return;
    setBusy(true); setErr(''); setOk('');
    post('/api/v1/sessions/spawn', { name: name || undefined, dir, node: host || undefined })
      .then((r) => {
        if (r.ok && r.data) {
          setOk(T.newSession.ok + ': ' + r.data.name);
          setTimeout(() => { close(); actions.addToast({ kind: 'info', text: T.newSession.ok + ': ' + r.data.name }); }, W6.spawnOkDelayMs);
          return;
        }
        const code = r.data && r.data.code;
        if (code === 'DIR_FORBIDDEN') setErr(T.newSession.forbidden + (r.data.detail ? ' (' + r.data.detail + ')' : ''));
        else if (code === 'DIR_NOT_FOUND') setErr(T.newSession.notFound + (r.data.detail ? ' (' + r.data.detail + ')' : ''));
        else if (code === 'NAME_IN_USE') { setNameUsed(true); setErr(T.newSession.nameTaken); }
        else if (code === 'NO_NODE') setErr(T.newSession.noNodes);
        else if (code === 'PRECHECK_FAILED') setErr(T.newSession.precheckFailed + (r.data.detail ? ': ' + r.data.detail : ''));
        else setErr((r.data && r.data.detail) || code || ('HTTP ' + r.status));
      })
      .catch(() => setErr('network'))
      .finally(() => setBusy(false));
  };

  if (!s.nodes.length) {
    return html`<${Dialog} title=${T.newSession.title} onClose=${close}
      footer=${Button({ variant: 'secondary', label: T.nodes.enClose, onClick: close })}>
      <p class="dialog-note">${T.newSession.noNodes}</p>
    </${Dialog}>`;
  }

  const showDirs = dirs.length > 0 && dirs.some((d) => d !== dir);
  return html`<${Dialog} title=${T.newSession.title} onClose=${close}
    footer=${html`
      <${Button} variant="secondary" label=${T.nodes.enClose} onClick=${close} />
      <${Button} variant="primary" label=${busy ? T.newSession.creating : T.newSession.create}
        loading=${busy} disabled=${!dir} onClick=${create} />`}>
    <div class="field">
      <label>${T.newSession.name}</label>
      <input value=${name} placeholder=${T.newSession.namePh}
        onInput=${(e) => { setName(e.target.value); setNameTouched(true); }} />
      ${nameUsed ? html`<div class="field-err">${T.newSession.nameTaken}</div>` : null}
    </div>
    <div class="field">
      <label>${T.newSession.node}</label>
      <select value=${host} onChange=${(e) => { setHost(e.target.value); setDirs([]); }}>
        ${s.nodes.map((n) => html`<option value=${n.host}>${n.host}</option>`)}
      </select>
    </div>
    <div class="field">
      <label>${T.newSession.dir}</label>
      <input ref=${dirRef} value=${dir} placeholder=${T.newSession.dirPh} autocomplete="off"
        onInput=${(e) => setDir(e.target.value)} />
      ${showDirs ? html`<ul class="dir-list">
        ${dirs.map((d) => html`<li key=${d} onClick=${() => setDir(d)}>${d}</li>`)}
      </ul>` : null}
      <div class="field-hint">${roots.length ? T.newSession.dirHintRoots(roots) : T.newSession.dirHint}</div>
    </div>
    ${s.gateway ? html`<div class="field">
      <label>${T.newSession.endpointTitle}</label>
      <div class="field-hint">${T.newSession.endpointShort(s.gateway.model_alias)}</div>
    </div>` : null}
    ${err ? html`<p class="dialog-err">${err}</p>` : null}
    ${ok ? html`<p class="dialog-ok">${ok}</p>` : null}
  </${Dialog}>`;
}
