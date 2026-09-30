// Пустое состояние (9.1:6, 9.3): иконка, одна фраза, одна основная кнопка.
import { html } from '../html.js';
import { Icon } from './icon-view.js';
import { Button } from './button.js';

export function EmptyState(props) {
  const { icon, title, text, actionLabel, onAction } = props;
  return html`<div class="empty-state">
    <div class="empty-icon">${Icon({ name: icon })}</div>
    <div class="empty-title">${title}</div>
    <div class="empty-text">${text}</div>
    ${onAction ? Button({ variant: 'primary', label: actionLabel, onClick: onAction }) : null}
  </div>`;
}
