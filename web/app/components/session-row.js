// Строка сессии в списках (В работе / Требуют внимания / Сессии): имя, узел,
// состояние (+ why.text), pin/prefer + кнопка снятия. Тон — state-style (9.4).
// why — уже локализованный текст (labels.whyText) или пусто. actions — кнопки
// справа (Остановить, Отозвать, …), onDelete — удаление GONE-записи.
import { html } from '../html.js';
import { stateColorVar } from '../state-style.js';
import { sessionStateLabel } from '../labels.js';
import { T } from '../i18n/ru.js';
import { post } from '../api.js';
import { actions } from '../store.js';
import { Badge } from './badge.js';
import { Button } from './button.js';
import { navigate } from '../router.js';

export function SessionRow(props) {
  const s = props.s;
  const meta = props.meta;
  const why = props.why || '';
  const onDelete = props.onDelete || null;
  const listActions = props.actions || []; // [{label, variant, onClick, loading}]
  const tone = stateColorVar(s.state);

  const unpin = () => {
    post('/api/v1/sessions/' + s.sid + '/unpin', {})
      .then((r) => {
        if (r.ok) actions.refresh();
        else actions.addToast({ kind: 'error', text: (r.data && r.data.detail) || 'unpin' });
      })
      .catch(() => actions.addToast({ kind: 'error', text: 'network' }));
  };

  const hasConstraint = s.constraint_kind && s.constraint_kind !== 'none';
  return html`<div class="srow srow--link" onClick=${() => navigate('sessions', s.sid)}>
    <div class="srow-main">
      <span class="srow-name" title=${s.sid}>${s.name || s.sid}</span>
      ${s.host ? html`<span class="srow-sub">${s.host}</span>` : null}
      ${s.server ? html`<span class="srow-sub srow-slot" title=${T.queue.ruleTitle + ': ' + (s.rule || '—')}>${s.server}${s.rule && T.queue.rules[s.rule] ? ' · ' + T.queue.rules[s.rule] : ''}</span>` : null}
      ${hasConstraint ? html`<span class="srow-sub srow-constraint">${s.constraint_kind === 'pin' ? T.a.pin : T.a.prefer}:${s.constraint_server}</span>` : null}
      ${why ? html`<span class="srow-why">${why}</span>` : null}
    </div>
    <div class="srow-side">
      <${Badge} tone=${tone} label=${sessionStateLabel(meta, s.state)} dot=${true} />
      ${listActions.map((a) => html`<${Button} variant=${a.variant || 'secondary'}
        label=${a.label} loading=${a.loading || false}
        onClick=${(e) => { e.stopPropagation(); a.onClick(); }} />`)}
      ${hasConstraint ? html`<${Button} variant="secondary" label=${T.a.unpin}
        onClick=${(e) => { e.stopPropagation(); unpin(); }} />` : null}
      ${onDelete ? html`<${Button} variant="danger" label=${T.session.a.deleteRow}
        onClick=${(e) => { e.stopPropagation(); onDelete(); }} />` : null}
    </div>
  </div>`;
}
