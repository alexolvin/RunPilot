// Hash-роутинг каркаса (/#/<page>[/<param>]). Без сборки и без server-side
// маршрутов — index.html раздаётся на /, страницы переключаются через
// location.hash. W4: подмаршруты /servers/<name> и /nodes/<host>.
import { actions, PAGES } from './store.js';

const PREFIX = '#/';

function parseHash() {
  const h = location.hash || '';
  const i = h.indexOf(PREFIX);
  if (i === -1) return { page: 'queue', param: null };
  const rest = h.slice(i + PREFIX.length).replace(/\/+$/, '');
  const parts = rest.split('/').filter(Boolean);
  const raw = parts.length ? parts[0] : 'queue';
  const page = PAGES.indexOf(raw) !== -1 ? raw : 'queue';
  const param = parts.length > 1 ? decodeURIComponent(parts[1]) : null;
  return { page, param };
}

export function navigate(page, param) {
  location.hash = PREFIX + page + (param ? '/' + encodeURIComponent(param) : '');
}

export function initRouter() {
  const r = parseHash();
  actions.setPage(r.page, r.param);
  window.addEventListener('hashchange', () => {
    const cur = parseHash();
    actions.setPage(cur.page, cur.param);
  });
}
