// Страница входа (web/login.html). Единственный HTML-маршрут без cookie.
// POST /web/login {token} → cookie + redirect (O4: ?next — запрошенная
// страница, иначе /). Ошибки — из ru.js (R2).
import { html } from '../html.js';
import { render } from 'preact';
import { useState } from 'preact/hooks';
import { HTTP } from '../constants.js';
import { T } from '../i18n/ru.js';

// O4 (11.1): возврат на запрошенную страницу (?next). Принимает только
// абсолютный путь /… (не //…) или hash-роут #/… — иначе /.
function nextPath() {
  const p = new URLSearchParams(location.search).get('next') || '/';
  if (p.startsWith('/') && !p.startsWith('//')) return p;
  if (p.startsWith('#/')) return p;
  return '/';
}

export function LoginView() {
  const [err, setErr] = useState('');
  const [token, setToken] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e) {
    e.preventDefault();
    if (busy) return;
    setErr('');
    setBusy(true);
    try {
      const r = await fetch('/web/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token }),
      });
      if (r.status === HTTP.unauthorized) { setErr(T.login.wrongToken); return; }
      if (r.status === HTTP.rateLimited) { setErr(T.login.rateLimited); return; }
      if (!r.ok) { setErr(T.login.error); return; }
      location.href = nextPath();
    } catch (e2) {
      setErr(T.login.error);
    } finally {
      setBusy(false);
    }
  }

  return html`<div class="login-page">
    <form class="card login-card" onSubmit=${submit}>
      <div class="login-brand">
        <div class="brand-mark"></div>
        <div>
          <div class="login-title">${T.login.title}</div>
          <div class="login-sub">${T.login.sub}</div>
        </div>
      </div>
      <div class="field">
        <label htmlFor="token">${T.login.tokenLabel}</label>
        <input id="token" type="password" autocomplete="off" required
          placeholder=${T.login.tokenPlaceholder}
          value=${token} onInput=${(e) => setToken(e.target.value)} />
      </div>
      <button class="btn btn--primary ${busy ? 'btn--loading' : ''}" type="submit"
        disabled=${busy}>${T.login.submit}</button>
      <div class="login-error">${err}</div>
    </form>
  </div>`;
}

render(html`<${LoginView} />`, document.getElementById('app'));
