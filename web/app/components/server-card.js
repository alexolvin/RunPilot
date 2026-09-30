// Карточка сервера (11.2/11.6): имя, состояние, слоты, KV, генерация.
// Тон — state-style (один код — один цвет, 9.4); клик — детальная страница.
import { html } from '../html.js';
import { stateColorVar } from '../state-style.js';
import { serverStateLabel, fmtPct, fmtTokS } from '../labels.js';
import { T } from '../i18n/ru.js';
import { Badge } from './badge.js';
import { navigate } from '../router.js';

export function ServerCard(props) {
  const s = props.server;
  const meta = props.meta;
  const tone = stateColorVar(s.state);
  return html`<div class="server-card card" role="link"
    onClick=${() => navigate('servers', s.name)}>
    <div class="server-card-head">
      <span class="server-dot" style=${{ '--tone': tone }}></span>
      <span class="server-card-name">${s.name}</span>
      ${s.removing ? html`<${Badge} tone="var(--color-warning)" label=${T.servers.removing} />` : null}
      <${Badge} tone=${tone} label=${serverStateLabel(meta, s.state)} />
    </div>
    <div class="server-card-metrics">
      <div class="metric">
        <span class="metric-k">${T.servers.slots}</span>
        <span class="metric-v">${s.running}/${s.total}</span>
      </div>
      <div class="metric">
        <span class="metric-k">${T.servers.kv}</span>
        <span class="metric-v">${s.missing ? T.units.none : fmtPct(s.kv_pct)}</span>
      </div>
      <div class="metric">
        <span class="metric-k">${T.servers.gen}</span>
        <span class="metric-v">${s.missing ? T.units.none : fmtTokS(s.gen_tok_s)}</span>
      </div>
    </div>
  </div>`;
}
