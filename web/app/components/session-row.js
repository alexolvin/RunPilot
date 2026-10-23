// Строка сессии в списках (В работе / Очередь / Внимание / Сессии): имя, узел,
// состояние (+ why.text), pin/prefer + кнопка снятия. Пиктограмма состояния
// (StateIcon) + кнопки-иконки (IconButton). Тон — state-style (9.4).
// why — уже локализованный текст (labels.whyText) или пусто. actions — кнопки
// справа [{icon, variant, onClick, loading, title}], onDelete — удаление GONE.
import { html } from '../html.js';
import { sessionStateLabel } from '../labels.js';
import { T } from '../i18n/ru.js';
import { post } from '../api.js';
import { actions } from '../store.js';
import { StateIcon } from './state-icon.js';
import { IconButton } from './icon-button.js';
import { navigate } from '../router.js';

export function SessionRow(props) {
  const s = props.s;
  const meta = props.meta;
  const why = props.why || '';
  const onDelete = props.onDelete || null;
  const listActions = props.actions || []; // [{icon, variant, onClick, loading, title}]

  const unpin = () => {
    post('/api/v1/sessions/' + s.sid + '/unpin', {})
      .then((r) => {
        if (r.ok) actions.refresh();
        else actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || 'unpin' });
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  };

  const hasConstraint = s.constraint_kind && s.constraint_kind !== 'none';
  // API-сессия (kind=API, api:<name>) → отдельная страница (эндпоинт + настройки).
  const page = s.kind === 'API' ? 'api-session' : 'sessions';
  return html`<div class="srow srow--link" onClick=${() => navigate(page, s.sid)}>
    <div class="srow-main">
      <span class="srow-name" title=${s.sid}>${s.name || s.sid}</span>
      ${s.node_id ? html`<span class="srow-sub">${s.node_id}</span>` : null}
      ${s.server ? html`<span class="srow-sub srow-slot" title=${T.queue.ruleTitle + ': ' + (s.rule || '—')}>${s.server}${s.rule && T.queue.rules[s.rule] ? ' · ' + T.queue.rules[s.rule] : ''}</span>` : null}
      ${hasConstraint ? html`<span class="srow-sub srow-constraint">${s.constraint_kind === 'pin' ? T.a.pin : T.a.prefer}:${s.constraint_server}</span>` : null}
      ${why ? html`<span class="srow-why">${why}</span>` : null}
    </div>
    <div class="srow-side">
      <${StateIcon} state=${s.state} title=${sessionStateLabel(meta, s.state)} />
      ${listActions.map((a) => html`<${IconButton} variant=${a.variant || 'secondary'}
        icon=${a.icon} title=${a.title || ''} loading=${a.loading || false}
        onClick=${(e) => { e.stopPropagation(); a.onClick(); }} />`)}
      ${hasConstraint ? html`<${IconButton} variant="secondary" icon="x" title=${T.a.unpin}
        onClick=${(e) => { e.stopPropagation(); unpin(); }} />` : null}
      ${onDelete ? html`<${IconButton} variant="danger" icon="x" title=${T.session.a.deleteRow}
        onClick=${(e) => { e.stopPropagation(); onDelete(); }} />` : null}
    </div>
  </div>`;
}
