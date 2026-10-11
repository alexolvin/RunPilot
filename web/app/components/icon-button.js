// IconButton — кнопка-иконка (замена текстовой кнопки в строках списков).
// Тон через variant (danger/secondary); подсказка — title. Иконка — Lucide.
import { html } from '../html.js';
import { Icon } from './icon-view.js';

export function IconButton(props) {
  const {
    icon = '', variant = '', onClick, disabled = false,
    loading = false, title = '',
  } = props;
  let cls = 'btn btn--icon';
  if (variant) cls += ' btn--' + variant;
  if (loading) cls += ' btn--loading';
  return html`<button class=${cls} type="button"
    disabled=${disabled ? true : null}
    title=${title ? title : null}
    onClick=${onClick ? onClick : null}>
    <${Icon} name=${icon} />
  </button>`;
}
