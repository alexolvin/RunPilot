// Строка сессии в списках (В работе / Требуют внимания / Сессии): имя, узел,
// состояние (+ why.text). Тон — state-style (9.4). why — уже локализованный
// текст (labels.whyText) или пусто. actions — кнопки справа (Остановить,
// Отозвать, …), onDelete — удаление GONE-записи.
import { html } from '../html.js';
import { stateColorVar } from '../state-style.js';
import { sessionStateLabel } from '../labels.js';
import { T } from '../i18n/ru.js';
import { Badge } from './badge.js';
import { Button } from './button.js';
import { navigate } from '../router.js';

export function SessionRow(props) {
  const s = props.s;
  const meta = props.meta;
  const why = props.why || '';
  const onDelete = props.onDelete || null;
  const actions = props.actions || []; // [{label, variant, onClick, loading}]
  const tone = stateColorVar(s.state);
  return html`<div class="srow srow--link" onClick=${() => navigate('sessions', s.sid)}>
    <div class="srow-main">
      <span class="srow-name" title=${s.sid}>${s.name || s.sid}</span>
      ${s.host ? html`<span class="srow-sub">${s.host}</span>` : null}
      ${s.server ? html`<span class="srow-sub srow-slot" title=${T.queue.ruleTitle + ': ' + (s.rule || '—')}>${s.server}${s.rule && T.queue.rules[s.rule] ? ' · ' + T.queue.rules[s.rule] : ''}</span>` : null}
      ${why ? html`<span class="srow-why">${why}</span>` : null}
    </div>
    <div class="srow-side">
      <${Badge} tone=${tone} label=${sessionStateLabel(meta, s.state)} dot=${true} />
      ${actions.map((a) => html`<${Button} variant=${a.variant || 'secondary'}
        label=${a.label} loading=${a.loading || false}
        onClick=${(e) => { e.stopPropagation(); a.onClick(); }} />`)}
      ${onDelete ? html`<${Button} variant="danger" label=${T.session.a.deleteRow}
        onClick=${(e) => { e.stopPropagation(); onDelete(); }} />` : null}
    </div>
  </div>`;
}
