// Карточка сервера (11.2/11.6): имя, состояние, слоты, KV, генерация.
// Тон — state-style (один код — один цвет, 9.4); клик — детальная страница.
import { html } from '../html.js';
import { stateColorVar } from '../state-style.js';
import { serverStateLabel, fmtTokS } from '../labels.js';
import { T } from '../i18n/ru.js';
import { Badge } from './badge.js';
import { navigate } from '../router.js';

// slotStateLabel — состояние слота (W13 этап 2): свободен / занят <сессия> /
// внешняя нагрузка / охлаждение.
function slotStateLabel(meta, sl) {
  switch (sl.state) {
    case 'external':
      return T.servers.slotExternal;
    case 'cooldown':
      return T.servers.slotCooldown;
    case 'PENDING':
    case 'ACTIVE':
      return T.servers.slotBusy(sl.session || sl.slot);
    default:
      return T.servers.slotFree;
  }
}

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
        <span class="metric-v">${T.servers.freeOf(s.free, s.total)}</span>
      </div>
      <div class="metric">
        <span class="metric-k">${T.servers.gen}</span>
        <span class="metric-v">${s.missing ? T.units.none : fmtTokS(s.gen_tok_s)}</span>
      </div>
    </div>
    ${s.slots_info && s.slots_info.length
      ? html`<div class="server-card-slots">
          ${s.slots_info.map((sl) => html`<span class="slot-chip" data-state=${sl.state}>${slotStateLabel(meta, sl)}</span>`)}
        </div>`
      : null}
  </div>`;
}
