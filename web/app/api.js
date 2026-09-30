// Запросы к /api/v1/* и /web/* (v2 раздел 15). CSRF-заголовок X-RUNPILOT-CSRF для
// изменяющих методов (W1: с cookie без CSRF → 403). Токен ставится boot-ом
// из GET /web/session (или ответа входа). If-Match/Idempotency-Key — W5/W6.
// 401 вне страницы входа = истёкла сессия → вход с возвратом на ту же
// страницу (O4: ?next = текущий hash-роут).

import { HTTP } from './constants.js';

const csrfHeader = 'X-RUNPILOT-CSRF';

let csrfToken = '';
export function setCSRF(token) { csrfToken = token; }
export function getCSRF() { return csrfToken; }

function isMutation(method) {
  return method === 'POST' || method === 'PATCH' || method === 'PUT' || method === 'DELETE';
}

// request(path, {method, body}) → {ok, status, data}. data = parsed JSON или null.
export async function request(path, opts) {
  const method = (opts && opts.method) || 'GET';
  const headers = { 'Content-Type': 'application/json' };
  if (isMutation(method) && csrfToken !== '') {
    headers[csrfHeader] = csrfToken;
  }
  const init = { method, headers };
  if (opts && opts.body !== undefined) {
    init.body = JSON.stringify(opts.body);
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch (e) {
    return { ok: false, status: 0, data: null, network: true };
  }
  if (res.status === HTTP.unauthorized && !path.startsWith('/web/login')) {
    // O4: сессия входа истекла → вход с возвратом на ту же страницу
    // («страница» SPA — hash-роут; пустой hash = очередь = /).
    location.href = '/web/login?next=' + encodeURIComponent(location.hash || '/');
  }
  let data = null;
  try {
    const text = await res.text();
    if (text !== '') { data = JSON.parse(text); }
  } catch (e) {
    data = null;
  }
  return { ok: res.ok, status: res.status, data };
}

export const get = (path) => request(path);
export const post = (path, body) => request(path, { method: 'POST', body });
export const patch = (path, body) => request(path, { method: 'PATCH', body });
export const del = (path) => request(path, { method: 'DELETE' });

// qs(params) → query-строка (пустые значения пропускаются).
export function qs(params) {
  const parts = [];
  for (const [k, v] of Object.entries(params || {})) {
    if (v !== undefined && v !== null && v !== '') {
      parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(v)}`);
    }
  }
  return parts.length ? `?${parts.join('&')}` : '';
}

// session() → {csrf_token, actor, version, expires_at} | null (401 → null).
export async function session() {
  const r = await request('/web/session');
  if (!r.ok) return null;
  if (r.data && r.data.csrf_token !== undefined) setCSRF(r.data.csrf_token);
  return r.data;
}
