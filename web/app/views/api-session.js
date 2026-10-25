// Страница API-сессии (api:<name>): подключение к эндпоинту шлюза + настройки
// клиента + действия (остановка = dequeue, прекращение = удалить). Данные
// клиента — из /api/v1/api-clients/{name}; эндпоинт — из store (s.gateway).
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { navigate } from '../router.js';
import { W6, API_PREFIX } from '../constants.js';
import { T } from '../i18n/ru.js';
import { sessionStateLabel } from '../labels.js';
import { stateColorVar } from '../state-style.js';
import { get, post, patch, del } from '../api.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';
import { Section } from '../components/section.js';
import { EmptyState } from '../components/empty-state.js';
import { Dialog } from '../components/dialog.js';

// CLASSES — допустимые классы очереди (model.QueueClass).
const CLASSES = ['resume', 'high', 'normal', 'low', 'batch'];

export function ApiSessionPage() {
  const s = useStore();
  const sid = s.pageParam || '';
  const name = sid.startsWith(API_PREFIX) ? sid.slice(API_PREFIX.length) : sid;
  const sess = s.sessions.find((x) => x.sid === sid);
  const [client, setClient] = useState(null);
  const [loadErr, setLoadErr] = useState('');
  // Форма настроек (class, priority, allowed_servers, таймауты).
  const [form, setForm] = useState(null);
  const [saving, setSaving] = useState(false);
  // Ключ: перевыпуск показывает один раз.
  const [newKey, setNewKey] = useState('');
  const [keyCopied, setKeyCopied] = useState(false);
  // Пример (curl/env) — копирование.
  const [exCopied, setExCopied] = useState(false);
  // Прекращение: подтверждение.
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [busy, setBusy] = useState(false);
  // Остановка: снимает с очереди (dequeue).
  const [stopping, setStopping] = useState(false);

  const loadClient = () => {
    get(`/api/v1/api-clients/${encodeURIComponent(name)}`)
      .then((r) => {
        if (r.ok && r.data) {
          setClient(r.data);
          setLoadErr('');
          setForm({
            class: r.data.class || 'batch',
            priority: r.data.priority || 0,
            allowed_servers: (r.data.allowed_servers || []).join(', '),
            max_hold_sec: r.data.max_hold_sec || 0,
            sticky_idle_sec: r.data.sticky_idle_sec || 0,
            aging_sec: r.data.aging_sec || 0,
            allow_stream: !!r.data.allow_stream,
          });
        } else {
          setLoadErr((r.data && r.data.detail) || ('HTTP ' + r.status));
        }
      })
      .catch(() => setLoadErr('network'));
  };
  useEffect(() => { if (name) loadClient(); }, [name]);

  const gw = s.gateway || {};
  const baseUrl = gw.url ? gw.url.replace(/\/+$/, '') + '/v1' : '';
  const model = gw.model_alias || '';
  // Пример (curl): ключ не известен (не хранится) → плейсхолдер.
  const curlEx = baseUrl
    ? 'curl ' + baseUrl + '/chat/completions \\\n'
      + '  -H "Authorization: Bearer <KEY>" \\\n'
      + '  -H "Content-Type: application/json" \\\n'
      + '  -d \'{ "model": "' + (model || 'model') + '", "messages": [{ "role": "user", "content": "Hi" }] }\''
    : '';
  const envEx = baseUrl
    ? 'OPENAI_BASE_URL=' + baseUrl + '\nOPENAI_API_KEY=<KEY>'
    : '';

  const copy = async (text, done) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      done();
      setTimeout(() => done(false), W6.copyFlashMs);
    } catch { /* clipboard недоступен */ }
  };

  const saveSettings = () => {
    if (!form) return;
    setSaving(true);
    patch(`/api/v1/api-clients/${encodeURIComponent(name)}`, {
      class: form.class,
      priority: Number(form.priority) || 0,
      allowed_servers: form.allowed_servers.split(',').map((x) => x.trim()).filter(Boolean),
      max_hold_sec: Number(form.max_hold_sec) || 0,
      sticky_idle_sec: Number(form.sticky_idle_sec) || 0,
      aging_sec: Number(form.aging_sec) || 0,
      allow_stream: !!form.allow_stream,
    }).then((r) => {
      if (r.ok && r.data) {
        setClient(r.data);
        actions.addToast({ kind: 'success', text: T.apiSession.saved });
      } else {
        const d = r.data || {};
        actions.addToast({ kind: 'error', text: d.detail || d.code || T.apiSession.saveErr });
      }
    }).catch(() => actions.addToast({ kind: 'error', text: 'network' }))
      .finally(() => setSaving(false));
  };

  const reissue = () => {
    post(`/api/v1/api-clients/${encodeURIComponent(name)}/reissue`, {})
      .then((r) => {
        if (r.ok && r.data) {
          setNewKey(r.data.key || '');
          actions.addToast({ kind: 'success', text: T.queue.apiReissued });
        } else {
          const d = r.data || {};
          actions.addToast({ kind: 'error', text: d.detail || d.code || r.status });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  };

  // Остановка = снять с очереди (dequeue псевдо-сессии).
  const stop = () => {
    if (stopping || !sess) return;
    setStopping(true);
    post(`/api/v1/sessions/${encodeURIComponent(sid)}/dequeue`, {})
      .then((r) => {
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.apiSession.stopDone });
          window.__runpilotResync && window.__runpilotResync();
        } else {
          const d = r.data || {};
          actions.addToast({ kind: 'error', text: d.detail || d.code || T.apiSession.stopErr });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }))
      .finally(() => setStopping(false));
  };

  // Прекращение = удалить клиента + псевдо-сессию.
  const doDelete = () => {
    setBusy(true);
    del(`/api/v1/api-clients/${encodeURIComponent(name)}`)
      .then((r) => {
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.apiSession.terminated });
          window.__runpilotResync && window.__runpilotResync();
          navigate('queue');
        } else {
          const d = r.data || {};
          actions.addToast({ kind: 'error', text: d.detail || d.code || T.apiSession.terminateErr });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }))
      .finally(() => { setBusy(false); setConfirmDelete(false); });
  };

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '320px' }}></div></div>`;
  }
  if (!sess) {
    return html`<div class="page">
      <${EmptyState} icon="users" title=${T.apiSession.notFound}
        text="" actionLabel=${T.apiSession.back} onAction=${() => navigate('queue')} />
    </div>`;
  }
  const tone = stateColorVar(sess.state);

  return html`<div class="page session-card">
    <div class="sc-head">
      <${Button} variant="text" label=${T.apiSession.back} onClick=${() => navigate('queue')} />
      <div class="sc-title">
        <span class="sc-name">${sess.name}</span>
        <${Badge} tone=${tone} label=${sessionStateLabel(s.meta, sess.state)} dot=${true} />
      </div>
      <div class="sc-meta mono">${sid}</div>
    </div>

    <div class="sc-body">
      <div class="sc-left sc-col">
        ${loadErr ? html`<${EmptyState} icon="alert" title=${T.apiSession.notFound}
          text=${loadErr} actionLabel=${T.apiSession.back} onAction=${() => navigate('queue')} />`
          : !client ? html`<div class="skeleton" style=${{ height: '120px' }}></div>`
          : html`
            <${Section} title=${T.apiSession.endpoint}>
              <p class="sc-note">${T.apiSession.endpointNote}</p>
              <dl class="sc-dl">
                <div><dt>${T.apiSession.baseUrl}</dt><dd class="mono">${baseUrl || '—'}</dd></div>
                <div><dt>${T.apiSession.model}</dt><dd class="mono">${model || '—'}</dd></div>
              </dl>
              <div class="api-key" style=${{ marginTop: '8px' }}>
                <span>${T.apiSession.key}</span>
                <span class="api-key-hint">${T.apiSession.keyHidden}</span>
                <${Button} variant="secondary" label=${T.apiSession.keyReissue} onClick=${reissue} />
              </div>
              ${newKey ? html`<div class="api-key"><span>${T.apiSession.keyReissued}</span>
                <code class="mono">${newKey}</code>
                <${Button} variant="text" label=${keyCopied ? T.apiSession.copied : T.apiSession.copy}
                  onClick=${() => copy(newKey, (v) => setKeyCopied(true))} />
              </div>` : null}
            </${Section}>

            ${curlEx ? html`<${Section} title=${T.apiSession.example}>
              <pre class="api-ex mono">${curlEx}</pre>
              <${Button} variant="text" label=${exCopied ? T.apiSession.copied : T.apiSession.copy}
                onClick=${() => { copy(curlEx, () => setExCopied(true)); }} />
              <p class="sc-note" style=${{ marginTop: '8px' }}>${T.apiSession.exampleEnv}</p>
              <pre class="api-ex mono">${envEx}</pre>
            </${Section}>` : null}

            <${Section} title=${T.apiSession.stats}>
              <dl class="sc-dl">
                <div><dt>${T.apiSession.requests}</dt><dd class="mono">${client.requests_24h}</dd></div>
                <div><dt>${T.apiSession.tokens}</dt><dd class="mono">${(client.prompt_tokens_24h || 0) + (client.completion_tokens_24h || 0)}</dd></div>
              </dl>
            </${Section}>
          `}
      </div>

      <div class="sc-right sc-col">
        ${form ? html`<${Section} title=${T.apiSession.settings}>
          <div class="api-form">
            <label class="field">
              <span class="field-label">${T.apiSession.cls}</span>
              <select class="input" value=${form.class}
                onChange=${(e) => setForm({ ...form, class: e.target.value })}>
                ${CLASSES.map((c) => html`<option value=${c}>${c}</option>`)}
              </select>
            </label>
            <label class="field">
              <span class="field-label">${T.apiSession.priority}</span>
              <input class="input" type="number" value=${form.priority}
                onInput=${(e) => setForm({ ...form, priority: e.target.value })} />
            </label>
            <label class="field">
              <span class="field-label">${T.apiSession.allowedServers}</span>
              <input class="input" type="text" value=${form.allowed_servers}
                placeholder=${T.apiSession.allowedServersHint}
                onInput=${(e) => setForm({ ...form, allowed_servers: e.target.value })} />
            </label>
            <div class="api-form-row">
              <label class="field">
                <span class="field-label">${T.apiSession.maxHoldSec}</span>
                <input class="input" type="number" value=${form.max_hold_sec}
                  onInput=${(e) => setForm({ ...form, max_hold_sec: e.target.value })} />
              </label>
              <label class="field">
                <span class="field-label">${T.apiSession.stickyIdleSec}</span>
                <input class="input" type="number" value=${form.sticky_idle_sec}
                  onInput=${(e) => setForm({ ...form, sticky_idle_sec: e.target.value })} />
              </label>
            </div>
            <label class="field">
              <span class="field-label">${T.apiSession.agingSec}</span>
              <input class="input" type="number" value=${form.aging_sec}
                onInput=${(e) => setForm({ ...form, aging_sec: e.target.value })} />
            </label>
            <label class="field api-check">
              <input type="checkbox" checked=${!!form.allow_stream}
                onChange=${(e) => setForm({ ...form, allow_stream: e.target.checked })} />
              <span class="field-label">${T.apiSession.allowStream}</span>
            </label>
            <p class="sc-note">${T.apiSession.allowStreamNote}</p>
            <${Button} variant="primary" label=${T.apiSession.save} loading=${saving}
              onClick=${saveSettings} />
          </div>
        </${Section}>` : null}

        <${Section} title=${T.session.actions}>
          <div class="sc-actions">
            ${sess.state === 'QUEUED' ? html`<${Button} variant="secondary"
              label=${T.apiSession.stop} loading=${stopping} onClick=${stop} />` : null}
            <${Button} variant="danger" label=${T.apiSession.terminate}
              onClick=${() => setConfirmDelete(true)} />
          </div>
        </${Section}>
      </div>
    </div>

    ${confirmDelete ? html`<${Dialog} title=${T.apiSession.terminateTitle}
      onClose=${() => setConfirmDelete(false)}
      footer=${html`
        <${Button} variant="secondary" label=${T.dialog.close}
          onClick=${() => setConfirmDelete(false)} />
        <${Button} variant="danger" label=${T.apiSession.terminateOk} loading=${busy}
          onClick=${doDelete} />`}>
      <p class="dialog-note">${T.apiSession.terminateNote.replace('{name}', sess.name)}</p>
    </${Dialog}>` : null}
  </div>`;
}
