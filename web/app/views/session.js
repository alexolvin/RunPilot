// Карточка сессии (11.3, v2): две колонки (десктоп) / одна (телефон).
// Экран кодера (живой, только чтение) + сведения + почему ждёт + запрос
// разрешения + действия + композер (13.1). Данные — из store (SSE); ввод и
// действия — через общий разбор команд /api/v1/command и /paste (R6: только по
// запросу оператора).
import { html } from '../html.js';
import { useState } from 'preact/hooks';
import { useStore, actions } from '../store.js';
import { navigate } from '../router.js';
import { T } from '../i18n/ru.js';
import { sessionStateLabel, classLabel, whyText, fmtTime } from '../labels.js';
import { stateColorVar } from '../state-style.js';
import { post, del } from '../api.js';
import { Badge } from '../components/badge.js';
import { Button } from '../components/button.js';
import { Section } from '../components/section.js';
import { EmptyState } from '../components/empty-state.js';
import { LiveScreen } from '../components/live-screen.js';
import { TerminalInline } from '../components/terminal.js';
import { Composer } from '../components/composer.js';
import { Dialog } from '../components/dialog.js';

// runCommand — команда через общий разбор (web = Telegram).
async function runCommand(sid, line) {
  const r = await post('/api/v1/command', { line: `${line} ${sid}`.trim() });
  const code = (r.data && r.data.code) || 'ERROR';
  const text = (r.data && r.data.text) || (r.data && r.data.detail) || '';
  actions.addToast({ kind: code === 'OK' ? 'info' : 'error', text: text || code });
}

function constraintLabel(sess) {
  if (sess.constraint_kind === 'pin') return 'pin:' + (sess.constraint_server || '');
  if (sess.constraint_kind === 'prefer') return 'prefer:' + (sess.constraint_server || '');
  return T.units.none;
}

function actionList(sess) {
  const st = sess.state;
  const a = [];
  // GONE: команд нет — только «Удалить запись» (ниже, в actions-секции).
  if (st === 'GONE') return a;
  if (st === 'IDLE' || st === 'HOLD') a.push({ line: '/enqueue', label: T.session.a.enqueue, v: 'secondary' });
  if (st === 'QUEUED') {
    a.push({ line: '/dequeue', label: T.session.a.dequeue, v: 'secondary' });
    a.push({ line: '/requeue', label: T.session.a.requeue, v: 'secondary' });
  }
  if (st !== 'HOLD' && st !== 'GONE') a.push({ line: '/hold', label: T.session.a.hold, v: 'secondary' });
  if (st === 'HOLD') a.push({ line: '/unhold', label: T.session.a.unhold, v: 'secondary' });
  if (['RUNNING', 'DISPATCHING', 'DETACHED', 'QUEUED'].includes(st)) a.push({ line: '/cancel', label: T.session.a.cancel, v: 'danger' });
  a.push({ line: '/compress', label: T.session.a.compress, v: 'secondary' });
  return a;
}

export function SessionPage() {
  const s = useStore();
  const sid = s.pageParam;
  // 5.4: подтверждение закрытия / удаления записи (hooks — до ранних return).
  const [confirmClose, setConfirmClose] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [busy, setBusy] = useState(false);
  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '320px' }}></div></div>`;
  }
  const sess = s.sessions.find((x) => x.sid === sid);
  if (!sess) {
    return html`<div class="page">
      <${EmptyState} icon="users" title=${T.session.notFound}
        text="" actionLabel=${T.session.back} onAction=${() => navigate('sessions')} />
    </div>`;
  }
  const pane = s.panes[sid];
  const tone = stateColorVar(sess.state);
  const why = sess.state === 'HOLD' ? whyText(s.meta, 'hold', sess.hold_reason) : '';
  const isPrompt = pane && pane.state === 'PROMPT';
  const acts = actionList(sess);

  // 5.4: «Закрыть» — kill tmux-сессии на узле (через общий разбор команд).
  const doClose = () => {
    setBusy(true);
    runCommand(sid, '/close').finally(() => { setBusy(false); setConfirmClose(false); });
  };
  // 5.4: «Удалить запись» — DELETE только для GONE (панели уже нет).
  const doDelete = () => {
    setBusy(true);
    del('/api/v1/sessions/' + sess.sid)
      .then((r) => {
        if (r.ok) {
          actions.addToast({ kind: 'info', text: T.session.deleteDone });
          window.__runpilotResync();
          navigate('sessions');
        } else {
          const d = r.data || {};
          actions.addToast({ kind: 'error', text: d.detail || d.code || ('HTTP ' + r.status) });
        }
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }))
      .finally(() => { setBusy(false); setConfirmDelete(false); });
  };

  return html`<div class="page session-card">
    <div class="sc-head">
      <${Button} variant="text" label=${'← ' + T.session.back} onClick=${() => navigate('sessions')} />
      <div class="sc-title">
        <span class="sc-name">${sess.name}</span>
        <${Badge} tone=${tone} label=${sessionStateLabel(s.meta, sess.state)} dot=${true} />
      </div>
      <div class="sc-meta mono">
        ${sess.host || T.units.none} · ${fmtTime(sess.created_at)} ·
        ${classLabel(s.meta, sess.class)} · ${constraintLabel(sess)} ·
        ${T.session.attempts} ${sess.attempts}
      </div>
    </div>

    <div class="sc-body">
      <div class="sc-left">
        ${sess.state === 'GONE'
          ? html`<${LiveScreen} sid=${sid} />`
          : html`<${TerminalInline} sid=${sid} />`}
        ${sess.state !== 'GONE' ? html`<${Button} variant="secondary" label=${T.session.openTerminal}
          onClick=${() => actions.openTerminal(sid)} />` : null}
      </div>

      <div class="sc-right">
        <${Section} title=${T.session.details}>
          <dl class="sc-dl">
            <div><dt>${T.session.node}</dt><dd class="mono">${sess.host || T.units.none}</dd></div>
            <div><dt>${T.session.sid}</dt><dd class="mono">${sess.sid}</dd></div>
            <div><dt>${T.session.version}</dt><dd>
              ${sess.agent_version || T.units.none}
              ${sess.untested ? html`<${Badge} tone="var(--color-warning)" label=${T.session.untested} />` : null}
            </dd></div>
            <div><dt>${T.session.auto}</dt><dd>
              <${Button} variant="text"
                label=${sess.auto ? T.mode.NORMAL : T.mode.PAUSED}
                onClick=${() => runCommand(sid, `/auto ${sess.auto ? 'off' : 'on'}`)} />
            </dd></div>
          </dl>
        </${Section}>

        ${why ? html`<${Section} title=${T.session.whyTitle}>
          <div class="sc-why">${why}</div>
        </${Section}>` : null}

        ${isPrompt ? html`<${Section} title=${T.session.approvalTitle} class="sc-approval">
          <div class="sc-approval-box">
            <p class="sc-approval-text">${pane.input_preview || T.session.approvalTitle}</p>
            <${Button} variant="primary" label=${T.session.approve}
              onClick=${() => runCommand(sid, '/approve allow')} />
          </div>
        </${Section}>` : null}

        <${Section} title=${T.session.actions}>
          <div class="sc-actions">
            ${acts.map((a) => html`<${Button} variant=${a.v} label=${a.label}
              onClick=${() => runCommand(sid, a.line)} />`)}
            ${sess.state !== 'GONE' ? html`<${Button} variant="danger"
              label=${T.session.a.close} onClick=${() => setConfirmClose(true)} />` : null}
            ${sess.state === 'GONE' ? html`<${Button} variant="danger"
              label=${T.session.a.delete} onClick=${() => setConfirmDelete(true)} />` : null}
          </div>
        </${Section}>
      </div>
    </div>

    <div class="sc-composer">
      <${Composer} sid=${sid} disabled=${false} />
    </div>

    ${confirmClose ? html`<${Dialog} title=${T.session.closeTitle}
      onClose=${() => setConfirmClose(false)}
      footer=${html`
        <${Button} variant="secondary" label=${T.dialog.close}
          onClick=${() => setConfirmClose(false)} />
        <${Button} variant="danger" label=${T.session.closeOk} loading=${busy}
          onClick=${doClose} />`}>
      <p class="dialog-note">${T.session.closeNote.replace('{name}', sess.name)}</p>
    </${Dialog}>` : null}

    ${confirmDelete ? html`<${Dialog} title=${T.session.deleteTitle}
      onClose=${() => setConfirmDelete(false)}
      footer=${html`
        <${Button} variant="secondary" label=${T.dialog.close}
          onClick=${() => setConfirmDelete(false)} />
        <${Button} variant="danger" label=${T.session.deleteOk} loading=${busy}
          onClick=${doDelete} />`}>
      <p class="dialog-note">${T.session.deleteNote.replace('{name}', sess.name)}</p>
    </${Dialog}>` : null}
  </div>`;
}
