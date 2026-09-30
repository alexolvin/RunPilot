// Переключатель темы (9.5): авто / светлая / тёмная, хранится в браузере.
// В стеке скриншотов тема задаётся детерминированно через localStorage runpilot-theme.
import { html } from '../html.js';
import { useStore, actions } from '../store.js';
import { T } from '../i18n/ru.js';
import { Icon } from './icon-view.js';

const ORDER = ['auto', 'light', 'dark'];

export function ThemeToggle() {
  const s = useStore();
  const icon = s.theme === 'dark' ? 'moon' : 'sun';
  const current = T.theme[s.theme] || T.theme.auto;
  function cycle() {
    const i = ORDER.indexOf(s.theme);
    actions.setTheme(ORDER[(i + 1) % ORDER.length]);
  }
  return html`<button class="icon-btn" type="button" title=${T.topbar.theme + ': ' + current} onClick=${cycle}>${Icon({ name: icon })}</button>`;
}
