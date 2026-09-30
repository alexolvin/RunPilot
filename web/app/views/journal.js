// Журнал (11.8): вкладки «События»/«Ходы»/«Аудит», серверная пагинация
// (id-cursor, web.page_size), экспорт CSV. Данные — GET /api/v1/journal (R3).
import { html } from '../html.js';
import { useState, useEffect } from 'preact/hooks';
import { useStore } from '../store.js';
import { get, qs } from '../api.js';
import { T } from '../i18n/ru.js';
import { fmtTime, fmtDur } from '../labels.js';
import { UI } from '../constants.js';
import { Button } from '../components/button.js';
import { SectionEmpty } from '../components/section.js';
import { navigate } from '../router.js';

const KINDS = ['events', 'turns', 'audit'];

// fmtPayload — человекочитаемый payload события (без «[object Object]», 19.3.4).
function fmtPayload(p) {
  if (p === null || p === undefined || p === '') return '';
  if (typeof p === 'string') return p;
  return JSON.stringify(p);
}

function tabLabel(k) {
  if (k === 'events') return T.journal.events;
  if (k === 'turns') return T.journal.turns;
  return T.journal.audit;
}

export function JournalPage() {
  const s = useStore();
  const [kind, setKind] = useState('events');
  const [rows, setRows] = useState([]);
  const [next, setNext] = useState(0);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    setLoading(true);
    setRows([]);
    setNext(0);
    get('/api/v1/journal' + qs({ kind, limit: UI.journalPageSize }))
      .then((r) => {
        setLoading(false);
        if (r.ok && r.data) {
          setRows(r.data.rows || []);
          setNext(r.data.next_cursor || 0);
        }
      });
  }, [kind]);

  function loadMore() {
    setLoading(true);
    get('/api/v1/journal' + qs({ kind, cursor: next, limit: UI.journalPageSize }))
      .then((r) => {
        setLoading(false);
        if (r.ok && r.data) {
          setRows((prev) => prev.concat(r.data.rows || []));
          setNext(r.data.next_cursor || 0);
        }
      });
  }

  if (!s.loaded) {
    return html`<div class="page"><div class="skeleton" style=${{ height: '200px' }}></div></div>`;
  }

  return html`<div class="page">
    <div class="tabs">
      ${KINDS.map((k) => html`<button class="tab-btn ${k === kind ? 'is-active' : ''}"
        type="button" onClick=${() => setKind(k)}>${tabLabel(k)}</button>`)}
      ${html`<a class="btn btn--text j-csv"
        href="/api/v1/journal?kind=${kind}&format=csv">${T.journal.csv}</a>`}
    </div>

    ${rows.length
      ? html`<div class="jtable">
          <div class="jrow jrow-head">
            <span>${T.journal.colTime}</span><span>${T.journal.colKind}</span>
            <span>${T.journal.colTarget}</span><span>${T.journal.colDetail}</span>
          </div>
          ${rows.slice(0, UI.journalRowsMax).map((r) => kind === 'turns'
            ? html`<div class="jrow">
                <span class="j-ts">${fmtTime(r.ts)}</span>
                <span class="j-kind">${r.outcome}</span>
                <span class="j-target" title=${r.sid}>${r.session || r.sid || T.units.none}</span>
                <span class="j-detail">${(r.servers || []).join(', ')} · ${fmtDur(r.dur_sec)}</span>
              </div>`
            : html`<div class="jrow">
                <span class="j-ts">${fmtTime(r.ts)}</span>
                <span class="j-kind">${r.kind}</span>
                <span class="j-target" title=${r.sid || undefined}>${r.sid || r.server || T.units.none}</span>
                <span class="j-detail" title=${fmtPayload(r.payload)}>
                  ${fmtPayload(r.payload) || T.units.none}</span>
              </div>`)}
        </div>`
      : html`<${SectionEmpty} text=${loading ? '' : T.journal.empty} />`}

    ${next > 0
      ? html`<${Button} label=${T.journal.loadMore} onClick=${loadMore} loading=${loading} />`
      : null}
  </div>`;
}
