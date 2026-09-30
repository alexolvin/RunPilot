// Стор SPA (v2 раздел 18: один store: состояние + один SSE-поток + мета +
// тема + режим + online + тосты). Живые данные обновляются ЕДИНСТВЕННО из
// SSE-потока (/api/v1/events) и полного среза (/api/v1/state) — без
// периодического REST-поллинга (R3/строгое правило 2). События применяются в
// ОДНОМ месте (applyEvent) — событие → DOM ≤ 250 мс (R8).
import { useState, useEffect } from 'preact/hooks';
import { get } from './api.js';
import { UI } from './constants.js';

// Экраны (навигация).
export const PAGES = [
  'queue', 'sessions', 'servers', 'nodes',
  'journal', 'monitoring', 'notifications', 'settings',
];
const THEME_KEY = 'runpilot.theme';
const MODE_KEY = 'runpilot.mode';

// S — состояние приложения (замораживается set(); подписчики получают новый
// объект). sessions/servers/nodes — из /api/v1/state; sseId — id последнего
// применённого SSE-события (маркер для R8).
let S = {
  page: 'queue',
  pageParam: null, // подмаршрут (/servers/<name>, /nodes/<host>)
  theme: 'light',
  mode: 'NORMAL',
  counts: { queue: 0, attention: 0, nodes: 0, servers: 0, notifications: 0 },
  meta: null,
  online: true,
  session: { actor: '', version: '' },
  toasts: [],
  loaded: false,
  // Живые данные (W4): один источник — /state + SSE.
  sessions: [],
  servers: [],
  nodes: [],
  gateway: null, // {url, model_alias} — эндпоинт шлюза (W9 доп-3c)
  panes: {}, // sid → {state, input_empty, input_preview, host} (W5: карточка)
  unmanaged: [], // 8.3 X1: «вне runpilot» — {pane_id, session, dir, cmd, host}
  closed: [], // GONE-сессии (показываются по фильтру «Показать закрытые»)
  notifications: [],
  sseId: 0,
  resyncing: false,
  // W6 (11.5): глобальный диалог (открывается из топбара/карточки):
  // {name, payload} | null.
  dialog: null,
  // W8 (13.4): терминал в браузере — открытая панель. sid | null.
  terminal: null,
};

function set(patch) {
  S = { ...S, ...patch };
  listeners.forEach((l) => l());
}

const listeners = new Set();
function subscribe(l) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// useStore — реактивная подписка на срез (Preact). preact/hooks не даёт
// useSyncExternalStore, поэтому подписка через useEffect + force-rerender:
// при каждом set() счётчик растёт, компонент перечитывает актуальный S.
export function useStore() {
  const [, setTick] = useState(0);
  useEffect(() => subscribe(() => setTick((x) => x + 1)), []);
  return S;
}

// --- нормализация среза /api/v1/state (ключи сессий — из SessionRecord) ---

function normSession(s) {
  return {
    sid: s.SID, name: s.Name, host: s.Host, state: s.State,
    hold_reason: s.HoldReason || '', class: s.Class,
    state_changed_at: s.StateChangedAt, last_server: s.LastServer || '',
    agent_version: s.AgentVersion || '', profile: s.Profile || '',
    constraint_kind: s.ConstraintKind || '', constraint_server: s.ConstraintServer || '',
    auto: !!s.AutoEnqueue, attempts: s.Attempts || 0,
    created_at: s.CreatedAt || '', tmux_session: s.TmuxSession || '',
    untested: !!s.untested,
  };
}
function normServer(s) {
  const total = s.slots || 0;
  const used = s.used || 0;
  return {
    name: s.name, state: s.state, running: used, total, free: total - used,
    kv_pct: s.kv_pct, gen_tok_s: s.gen_tok_s,
    gpu: s.gpu, vram_used_gb: s.vram_used_gb, vram_total_gb: s.vram_total_gb,
    ext: s.ext, slots_info: s.slots_info || [],
    removing: s.removing || false,
    missing: s.metrics_missing || false,
  };
}
function normNode(n) {
  const h = n.health;
  return {
    host: n.Host, host_ip: n.HostIP, runpilot_version: n.RUNPILOTVersion,
    tmux_version: n.TmuxVersion, os: n.OS, sockets: n.Sockets || [],
    // W7 (14.4, N9/N10): здоровье узла — проблемы и расхождение часов.
    health: h
      ? {
          problems: h.problems || [],
          time_skew_ms: h.time_skew_ms || 0,
          disk_free_mb: h.disk_free_mb || 0,
          qwen_version: h.qwen_version || '',
        }
      : null,
  };
}
// paneMap — снимки панелей (W5 карточка): sid → состояние/ввод/узел.
// /state отдаёт PaneInfo без json-тегов: {Pane:{sid,state,…}, Host, …}.
function paneMap(data) {
  const m = {};
  for (const p of data.panes || []) {
    const pane = p.Pane || p.pane || {};
    if (pane.sid) {
      m[pane.sid] = {
        state: pane.state, input_empty: !!pane.input_empty,
        input_preview: pane.input_preview || '', host: p.Host || p.host || '',
      };
    }
  }
  return m;
}
// Счётчики вычисляются из ТЕКУЩЕГО среза (без опроса /notifications, R3):
// «уведомления» = сессии в HOLD + серверы не UP (то же, что выводит сервер).
function computeCounts(sessions, servers, nodes) {
  return {
    queue: sessions.filter((s) => s.state === 'QUEUED').length,
    attention: sessions.filter((s) => s.state === 'HOLD').length,
    nodes: nodes.length,
    servers: servers.length,
    notifications: sessions.filter((s) => s.state === 'HOLD').length
      + servers.filter((x) => x.state && x.state !== 'UP').length,
  };
}

// setFull — полный срез /api/v1/state (первичная загрузка и RESYNC).
function setFull(data) {
  const sessions = (data.sessions || []).map(normSession);
  const servers = (data.servers || []).map(normServer);
  const nodes = (data.nodes || []).map(normNode);
  const panes = paneMap(data);
  set({
    sessions, servers, nodes, panes,
    unmanaged: data.unmanaged || [],
    gateway: data.gateway || null,
    mode: data.mode || 'NORMAL',
    counts: computeCounts(sessions, servers, nodes),
    resyncing: false,
  });
}

// --- действия ---

export const actions = {
  setPage(page, param) {
    if (PAGES.includes(page)) set({ page, pageParam: param || null });
  },
  setMode(mode) {
    set({ mode });
    try { localStorage.setItem(MODE_KEY, mode); } catch { /* приватный режим */ }
  },
  setMeta(meta) {
    set({ meta });
  },
  setFull,
  // Список для экрана «Уведомления» (разовый GET). Счётчик вкладки/колокольки
  // — клиентский (computeCounts), сюда не трогается.
  setNotifications(list) {
    set({ notifications: list || [] });
  },
  // loadClosed — GONE-сессии (фильтр «Показать закрытые»): /state их скрывает,
  // берём все сессии и отбираем GONE.
  loadClosed() {
    return get('/api/v1/sessions?all=true').then((r) => {
      if (r.ok && r.data) {
        set({ closed: r.data.filter((x) => x.State === 'GONE').map(normSession) });
      }
    });
  },
  // removeClosed — локально убрать GONE-запись после DELETE.
  removeClosed(sid) {
    set({ closed: S.closed.filter((x) => x.sid !== sid) });
  },
  setOnline(online) {
    if (S.online !== online) set({ online });
  },
  setTheme(theme) {
    const root = document.documentElement;
    root.setAttribute('data-theme', theme);
    set({ theme });
    try { localStorage.setItem(THEME_KEY, theme); } catch { /* приватный режим */ }
  },
  // Тема: 'auto' → prefers-color-scheme, иначе светлая/тёмная.
  initTheme() {
    let saved = 'auto';
    try { saved = localStorage.getItem(THEME_KEY) || 'auto'; } catch { /* приватный режим */ }
    const resolve = () => (
      saved !== 'auto'
        ? saved
        : (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
    );
    const root = document.documentElement;
    root.setAttribute('data-theme', resolve());
    S = { ...S, theme: resolve() };
    try {
      const mq = window.matchMedia('(prefers-color-scheme: dark)');
      if (mq && saved === 'auto' && mq.addEventListener) {
        mq.addEventListener('change', () => {
          root.setAttribute('data-theme', resolve());
          S = { ...S, theme: resolve() };
          listeners.forEach((l) => l());
        });
      }
    } catch { /* старый браузер */ }
  },
  setSession(actor, version) {
    set({ session: { actor, version } });
  },
  addToast(toast) {
    const id = (S.toasts.length ? Math.max(...S.toasts.map((t) => t.id)) : 0) + 1;
    const t = { id, ...toast };
    set({ toasts: [...S.toasts, t].slice(-UI.toastMax) });
    if (t.action) {
      setTimeout(() => actions.dismissToast(id), UI.toastActionTtlMs);
    } else if (t.kind !== 'info') {
      setTimeout(() => actions.dismissToast(id), UI.toastTtlMs);
    }
  },
  dismissToast(id) {
    set({ toasts: S.toasts.filter((t) => t.id !== id) });
  },
  setLoaded(loaded) {
    set({ loaded });
  },
  // W6 (11.5): глобальный диалог.
  openDialog(name, payload) {
    set({ dialog: { name, payload } });
  },
  closeDialog() {
    set({ dialog: null });
  },
  // W8 (13.4): терминал в браузере.
  openTerminal(sid) {
    if (sid) set({ terminal: sid });
  },
  closeTerminal() {
    set({ terminal: null });
  },

  // applyEvent — ЕДИНСТВЕННОЕ место применения SSE-событий к состоянию (R8).
  // Каждое событие двигает sseId (маркер для замера события→DOM) и точечно
  // обновляет сессию/сервер. RESYNC обрабатывает SSE-клиент (полный срез).
  // sseId — id кадра SSE (msg.lastEventId): в JSON-теле события id нет.
  // payload: SESSION_STATE {from,to}; HOLD {reason}; SERVER_STATE {to}.
  applyEvent(ev, sseId) {
    if (!ev || ev.kind === 'RESYNC') return;
    const pl = ev.payload || {};
    const patch = {};
    if (sseId) patch.sseId = sseId;
    if ((ev.kind === 'SESSION_STATE' || ev.kind === 'HOLD') && ev.sid &&
        !S.sessions.some((s) => s.sid === ev.sid) && !S.resyncing) {
      // Незнакомый sid: сессия создана, пока страница открыта (событие
      // создания несёт только состояние, без полей) — полный срез /state.
      doResync();
      return;
    }
    if (ev.kind === 'SESSION_STATE' && ev.sid && pl.to) {
      patch.sessions = S.sessions.map((s) =>
        s.sid === ev.sid ? { ...s, state: pl.to } : s);
    } else if (ev.kind === 'HOLD' && ev.sid && pl.reason) {
      patch.sessions = S.sessions.map((s) =>
        s.sid === ev.sid ? { ...s, hold_reason: pl.reason } : s);
    } else if (ev.kind === 'SERVER_STATE' && ev.server && pl.to) {
      patch.servers = S.servers.map((s) =>
        s.name === ev.server ? { ...s, state: pl.to } : s);
    }
    patch.counts = computeCounts(
      patch.sessions || S.sessions, patch.servers || S.servers, S.nodes);
    set(patch);
  },
  setResyncing(v) {
    if (S.resyncing !== v) set({ resyncing: v });
  },
};

// --- SSE-клиент (v2 15.4): один поток + досылка + RESYNC ---

// sseInstance — текущий EventSource (тестовый хук __runpilotSSE, CONTROL 2).
let sseInstance = null;

// doResync — полный срез /state (при RESYNC от сервера и по хуку __runpilotResync).
function doResync() {
  actions.setResyncing(true);
  return get('/api/v1/state').then((r) => {
    if (r.ok && r.data) actions.setFull(r.data);
  });
}

// lastEventId ведёт сам браузер (EventSource шлёт Last-Event-ID при
// переподключении). При RESYNC сервера клиент делает полный срез /state.
export function connectSSE() {
  if (typeof EventSource === 'undefined') return () => {};
  if (sseInstance) sseInstance.close(); // переподключение: закрыть предыдущее
  sseInstance = new EventSource('/api/v1/events');
  const es = sseInstance;
  es.onopen = () => actions.setOnline(true);
  es.onerror = () => actions.setOnline(false);
  es.onmessage = (msg) => {
    let ev;
    try { ev = JSON.parse(msg.data); } catch { return; }
    if (ev.kind === 'RESYNC') { doResync(); return; }
    // id события — в заголовке кадра SSE (msg.lastEventId), не в JSON-теле.
    actions.applyEvent(ev, msg.lastEventId ? Number(msg.lastEventId) : 0);
  };
  return () => { if (es) es.close(); };
}

// --- Тестовые хуки (CONTROL 2, раздел 19.4): состояние + SSE-клиент. ---
// Только для e2e-теста «разрыв SSE → состояние клиента == GET /api/v1/state»:
// чтение среза и ручной ресинк. Секретов нет (actor/version уже в DOM).
window.__runpilotState = () => S;
window.__runpilotResync = () => doResync();
window.__runpilotSSE = () => sseInstance;
window.__runpilotReconnectSSE = () => connectSSE();
// W8 (13.4): открыть/закрыть терминал (скриншоты + e2e).
window.__runpilotOpenTerminal = (sid) => actions.openTerminal(sid);
window.__runpilotCloseTerminal = () => actions.closeTerminal();
