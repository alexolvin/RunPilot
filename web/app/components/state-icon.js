// StateIcon — цветная пиктограмма состояния сессии (замена текстового бейджа).
// Тон — stateColorVar (9.4: один код — один цвет). Подсказка — подпись из meta
// (sessionStateLabel), передаётся вызывающим через title.
import { html } from '../html.js';
import { stateColorVar } from '../state-style.js';
import { Icon } from './icon-view.js';

// Состояние сессии → иконка Lucide.
const STATE_ICON = {
  RUNNING: 'play',
  DISPATCHING: 'activity',
  QUEUED: 'inbox',
  DETACHED: 'monitor',
  HOLD: 'triangle-alert',
  IDLE: 'moon',
  GONE: 'x',
};

export function StateIcon(props) {
  const state = props.state || '';
  const name = STATE_ICON[state] || 'activity';
  const tone = stateColorVar(state);
  const title = props.title || '';
  return html`<span class="state-icon" style=${{ color: tone }} title=${title || null}>
    <${Icon} name=${name} />
  </span>`;
}
