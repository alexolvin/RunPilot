// Компонент иконки (Lucide, stroke=currentColor; размер/линия — CSS .icon, 9.2).
// ICONS хранит ТОЛЬКО внутреннюю разметку (path/polyline/…); без <svg>-обёртки
// браузер отбрасывает её как неизвестные элементы — иконки не рендерятся.
import { html } from '../html.js';
import { ICONS } from './icon.js';

const svgOpen = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
  'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">';

export function Icon(props) {
  const name = props.name || '';
  const cls = props.cls || '';
  const inner = ICONS[name] || '';
  const markup = inner ? svgOpen + inner + '</svg>' : '';
  const title = props.title;
  // ВАЖНО: без object-spread в атрибутах (${attrs}) — vendored htm молча
  // выбрасывает его (props остаются пустыми, span рендерится без class/innerHTML).
  const clsAttr = 'icon' + (cls ? ' ' + cls : '');
  return title
    ? html`<span class=${clsAttr} title=${title} dangerouslySetInnerHTML=${{ __html: markup }}></span>`
    : html`<span class=${clsAttr} aria-hidden="true" dangerouslySetInnerHTML=${{ __html: markup }}></span>`;
}
