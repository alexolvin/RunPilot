// Секция экрана: заголовок (+ счётчик) + содержимое (11.x).
import { html } from '../html.js';

export function Section(props) {
  const { title, count, action, children } = props;
  return html`<section class="page-section">
    <div class="section-head">
      <h3 class="section-title">${title}</h3>
      ${count ? html`<span class="section-count">${count}</span>` : null}
      ${action || null}
    </div>
    ${children}
  </section>`;
}

// sectionTitle — только заголовок (для простых блоков).
export function SectionTitle(props) {
  const { title, count } = props;
  return html`<div class="section-head">
    <h3 class="section-title">${title}</h3>
    ${count ? html`<span class="section-count">${count}</span>` : null}
  </div>`;
}

// SectionEmpty — тихая заглушка внутри заполненной секции («нет данных»).
export function SectionEmpty(props) {
  return html`<div class="section-empty">${props.text || ''}</div>`;
}
