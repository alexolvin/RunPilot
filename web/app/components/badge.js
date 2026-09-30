// Бейдж (9.3): тон задаётся переменной --tone (state-style.js); подпись — из
// meta/ru.js. Один код — один цвет на всех экранах (9.4).
import { html } from '../html.js';

export function Badge(props) {
  const { tone, label, dot } = props;
  const style = tone ? { '--tone': tone } : undefined;
  return html`<span class="badge" ${style}>${dot ? html`<span class="dot"></span>` : null}${label}</span>`;
}
