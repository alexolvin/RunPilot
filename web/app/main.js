// Точка входа SPA (web/index.html). Поднимает тему и роутинг, разово
// подгружает мета и полный срез /state, затем включает ЕДИНСТВЕННЫЙ SSE-поток
// (живые обновления) и периодический опрос /meta (v2 15.4). Живые данные —
// только из SSE + /state (R3: без периодического REST-поллинга состояния).
import { render } from 'preact';
import { html } from './html.js';
import { App } from './app.js';
import { actions, connectSSE } from './store.js';
import { initRouter } from './router.js';
import { get, session } from './api.js';
import { UI } from './constants.js';

async function loadBootstrap() {
  const [metaR, stateR] = await Promise.all([get('/api/v1/meta'), get('/api/v1/state')]);
  if (metaR.ok && metaR.data) actions.setMeta(metaR.data);
  if (stateR.ok && stateR.data) actions.setFull(stateR.data);
  actions.setLoaded(true);
}

// Периодический опрос /meta (15.4): перечисления/единицы/версия — не живые
// данные, а справочник (R3 допускает периодический опрос /meta).
function pollMeta() {
  get('/api/v1/meta').then((r) => { if (r.ok && r.data) actions.setMeta(r.data); });
}

function boot() {
  actions.initTheme();
  initRouter();
  const root = document.getElementById('app');
  render(html`<${App} />`, root);
  session().then((s) => { if (s) actions.setSession(s.actor || '', s.version || ''); });
  loadBootstrap().then(() => {
    connectSSE();
    setInterval(pollMeta, UI.metaPollMs);
  });
}

boot();
