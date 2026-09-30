// Мониторинг (11.9): динамика по серверам (спарклайны окна), ходы за 24 часа,
// p95 задержки диспетчеризации и TTFB шлюза. Данные — GET /api/v1/monitoring.
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore } from '../store.js';
import { get } from '../api.js';
import { T } from '../i18n/ru.js';
import { stateColorVar } from '../state-style.js';
import { serverStateLabel, fmtMS, fmtDur } from '../labels.js';
import { Sparkline } from '../components/sparkline.js';
import { Stat } from '../components/stat.js';
import { Badge } from '../components/badge.js';
import { Section, SectionEmpty } from '../components/section.js';
import { EmptyState } from '../components/empty-state.js';
import { navigate } from '../router.js';

export function MonitoringPage() {
  const s = useStore();
  const [mon, setMon] = useState(null);
  useEffect(() => {
    get('/api/v1/monitoring').then((r) => { if (r.ok && r.data) setMon(r.data); });
  }, []);

  if (!s.loaded || !mon) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }
  if (!mon.servers.length && !s.servers.length) {
    return html`<div class="page">
      <${EmptyState} icon="activity" title=${T.empty.monitoring.title} text=${T.empty.monitoring.text}
        actionLabel=${T.empty.monitoring.action} onAction=${() => navigate('servers')} />
    </div>`;
  }

  return html`<div class="page">
    <${Section} title=${T.monitoring.windowDay} count=${mon.servers.length}>
      ${mon.servers.length
        ? mon.servers.map((m) => {
          const tone = stateColorVar(m.state);
          return html`<div class="mon-server card">
            <div class="server-card-head">
              <span class="server-dot" style=${{ '--tone': tone }}></span>
              <span class="server-card-name">${m.name}</span>
              <${Badge} tone=${tone} label=${serverStateLabel(s.meta, m.state)} />
            </div>
            <div class="spark-block">
              <div class="spark-row">
                <span class="spark-k">${T.monitoring.gen}</span>
                <${Sparkline} values=${(m.history || []).map((p) => p.gen_tok_s)} tone=${tone} />
              </div>
              <div class="spark-row">
                <span class="spark-k">${T.monitoring.kv}</span>
                <${Sparkline} values=${(m.history || []).map((p) => p.kv)} tone=${tone} />
              </div>
            </div>
          </div>`;
        })
        : html`<${SectionEmpty} text=${T.empty.monitoring.text} />`}
    </${Section}>

    <${Section} title=${T.monitoring.turns}>
      <div class="stat-grid">
        <${Stat} label=${T.queue.turns} value=${String(mon.turns_24h.turns)} />
        <${Stat} label=${T.monitoring.ok} value=${String(mon.turns_24h.ok)} />
        <${Stat} label=${T.monitoring.error} value=${String(mon.turns_24h.error)} />
        <${Stat} label=${T.monitoring.cancelled} value=${String(mon.turns_24h.cancelled)} />
        <${Stat} label=${T.monitoring.lost} value=${String(mon.turns_24h.lost)} />
        <${Stat} label=${T.monitoring.duration} value=${fmtDur(mon.turns_24h.avg_duration_sec)} />
      </div>
    </${Section}>

    <${Section} title=${T.monitoring.latency}>
      <div class="stat-grid">
        <${Stat} label=${T.monitoring.dispatch} value=${fmtMS(mon.latency.dispatch_p95_ms)} />
        <${Stat} label=${T.monitoring.ttfb} value=${fmtMS(mon.latency.gateway_ttfb_p95_ms)} />
      </div>
    </${Section}>
  </div>`;
}
