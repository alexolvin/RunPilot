// Привязка htm к preact (раздел 18: Preact 10 + htm, ESM, без сборки).
// Все компоненты используют html`…` и (при необходимости) h.
import htm from 'htm';
import { h } from 'preact';

export const html = htm.bind(h);
export { h };
