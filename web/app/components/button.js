// Кнопка (9.3): primary / secondary / danger / danger-fill / text; загрузка;
// неактивная кнопка показывает причину в подсказке (title из API/ru.js).
import { html } from '../html.js';

export function Button(props) {
  const {
    variant = '', label = '', onClick, disabled = false,
    loading = false, title = '',
  } = props;
  let cls = 'btn';
  if (variant) cls += ' btn--' + variant;
  if (loading) cls += ' btn--loading';
  // Класс — инлайн в шаблоне (htm-спред объекта с onClick/type ненадёжно
  // применяет class); обработчик/флаги — явные атрибуты, null = не выводить.
  return html`<button class=${cls} type="button"
    disabled=${disabled ? true : null}
    title=${title ? title : null}
    onClick=${onClick ? onClick : null}>${label}</button>`;
}
