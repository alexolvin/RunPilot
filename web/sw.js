// Service worker RunPilot (раздел 18): кэширует ТОЛЬКО оболочку (shell);
// API, /web/* и SSE НЕ кэшируются (всегда к серверу). Network-first.
// Регистрация — на этапе W9 (приёмка/PWA): в W3 не активируется, чтобы
// конвейер скриншотов был детерминированным (нет фоновых загрузок кэша).
const CACHE = 'runpilot-shell-v1';
const SHELL = [
  '/styles/tokens.css', '/styles/layout.css', '/styles/components.css',
  '/app/main.js', '/app/html.js', '/app/store.js', '/app/api.js', '/app/router.js',
  '/app/constants.js', '/app/state-style.js', '/app/i18n/ru.js', '/app/app.js',
  '/app/views/pages.js',
  '/app/components/icon.js', '/app/components/icon-view.js', '/app/components/badge.js',
  '/app/components/button.js', '/app/components/empty-state.js', '/app/components/sidebar.js',
  '/app/components/topbar.js', '/app/components/bottom-tabs.js', '/app/components/command-bar.js',
  '/app/components/theme-toggle.js', '/app/components/banner.js',
  '/vendor/preact.module.js', '/vendor/preact-hooks.module.js', '/vendor/htm.module.js',
  '/icon.svg',
];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).catch(() => {}));
  self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(clients.claim());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  // API, вход, SSE — всегда к серверу.
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/web/') || url.pathname === '/ws') return;
  e.respondWith(
    fetch(req)
      .then((res) => {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(req, copy)).catch(() => {});
        return res;
      })
      .catch(() => caches.match(req))
  );
});
