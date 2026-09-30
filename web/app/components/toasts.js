// Тосты (v2 11.11, O5/J12): всплывающие сообщения с опциональным действием
// («Отменить»). Рендерятся поверх контента; тост с action живёт дольше
// (UI.toastActionTtlMs), обычные — UI.toastTtlMs (info — до закрытия).
import { html } from '../html.js';
import { useStore, actions } from '../store.js';

function tone(kind) {
  if (kind === 'error') return 'var(--color-danger)';
  if (kind === 'success') return 'var(--color-success)';
  return 'var(--color-info)';
}

export function Toasts() {
  const s = useStore();
  if (!s.toasts || !s.toasts.length) return null;
  return html`<div class="toasts" role="status" aria-live="polite">
    ${s.toasts.map((t) => html`
      <div class="toast toast-${t.kind || 'info'}" style=${{ '--tone': tone(t.kind) }}>
        <span class="toast-text">${t.text}</span>
        ${t.action ? html`
          <button class="toast-action"
            onClick=${() => { if (t.action.fn) t.action.fn(); actions.dismissToast(t.id); }}>
            ${t.action.label}
          </button>` : null}
        <button class="toast-close" aria-label="✕"
          onClick=${() => actions.dismissToast(t.id)}>✕</button>
      </div>`)}
  </div>`;
}
