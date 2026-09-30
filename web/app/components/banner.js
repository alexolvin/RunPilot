// Баннер (9.3, 10.2): режим не NORMAL, безопасный режим, нет связи — на всю
// ширину над содержимым. W3 ( NORMAL, в сети) — не рендерится.
import { html } from '../html.js';
import { useStore } from '../store.js';
import { T } from '../i18n/ru.js';
import { Icon } from './icon-view.js';

function strip(tone, label) {
  return html`<div class="banner" style=${{ '--tone': tone }}>
    ${Icon({ name: 'triangle-alert' })}<span>${label}</span>
  </div>`;
}

export function Banner() {
  const s = useStore();
  if (!s.online) return strip('var(--color-danger)', T.banner.offline);
  if (s.mode === 'EMERGENCY') return strip('var(--color-danger)', T.banner.emergency);
  if (s.mode === 'SAFE_MODE') return strip('var(--color-danger)', T.banner.safeMode);
  if (s.mode === 'PAUSED') return strip('var(--color-warning)', T.banner.paused);
  if (s.resyncing) return strip('var(--color-info)', T.banner.resyncing);
  return null;
}
