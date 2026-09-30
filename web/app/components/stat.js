// Метрика-карточка (11.2 «Сегодня», 11.9): подпись + значение (+ подсказка).
import { html } from '../html.js';

export function Stat(props) {
  const { label, value, hint } = props;
  return html`<div class="stat card">
    <div class="stat-label">${label}</div>
    <div class="stat-value">${value}</div>
    ${hint ? html`<div class="stat-hint">${hint}</div>` : null}
  </div>`;
}
