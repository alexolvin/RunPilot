// Диалог (W6, 11.5/5.1/5.3/11.10): оверлей + панель; закрытие по Escape
// и клику по подложке. Рендерится внутри страницы — оверлей position:fixed
// накрывает вьюпорт независимо от места в дереве.
import { html } from '../html.js';
import { useEffect } from 'preact/hooks';
import { Icon } from './icon-view.js';
import { T } from '../i18n/ru.js';

export function Dialog(props) {
  const { title, onClose, footer, children, wide = false } = props;
  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);
  const cls = 'dialog' + (wide ? ' dialog--wide' : '');
  return html`<div class="dialog-overlay" onClick=${onClose}>
    <div class=${cls} role="dialog" aria-modal="true" aria-label=${title}
      onClick=${(e) => e.stopPropagation()}>
      <div class="dialog-head">
        <h2 class="dialog-title">${title}</h2>
        <button class="dialog-close" type="button" aria-label=${T.dialog.close}
          onClick=${onClose}>
          ${Icon({ name: 'x' })}
        </button>
      </div>
      <div class="dialog-body">${children}</div>
      ${footer ? html`<div class="dialog-foot">${footer}</div>` : null}
    </div>
  </div>`;
}
