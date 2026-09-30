// Каркас (frame): боковая панель + верхняя панель + баннер + контент +
// командная строка + нижние вкладки (телефон). Разметка — layout.css (10.1).
// W4: заголовок вкладки «(N) RunPilot» (11.11) и маркер data-sse-id для R8.
import { html } from './html.js';
import { useEffect } from 'preact/hooks';
import { useStore } from './store.js';
import { T } from './i18n/ru.js';
import { Sidebar } from './components/sidebar.js';
import { Topbar } from './components/topbar.js';
import { BottomTabs } from './components/bottom-tabs.js';
import { CommandBar } from './components/command-bar.js';
import { Banner } from './components/banner.js';
import { PageView } from './views/pages.js';
import { NewSessionDialog } from './components/new-session-dialog.js';
import { TerminalPage } from './components/terminal.js';
import { Toasts } from './components/toasts.js';

// W6 (11.5): глобальные диалоги (открываются из топбара/карточки).
function DialogHost() {
  const s = useStore();
  if (!s.dialog) return null;
  if (s.dialog.name === 'new-session') return html`<${NewSessionDialog} />`;
  return null;
}

// TitleUpd — заголовок вкладки «(N) RunPilot»: N = активные уведомления
// (сессии в HOLD + серверы не UP), клиентски из среза (без опроса).
function TitleUpd() {
  const s = useStore();
  useEffect(() => {
    const n = s.sessions.filter((x) => x.state === 'HOLD').length
      + s.servers.filter((x) => x.state && x.state !== 'UP').length;
    document.title = (n > 0 ? '(' + n + ') ' : '') + T.brand.name;
  }, [s.sessions, s.servers]);
  return null;
}

export function App() {
  const s = useStore();
  return html`<div class="app">
    <${TitleUpd} />
    ${Sidebar()}
    <div class="main">
      ${Topbar()}
      ${Banner()}
      <main class="content" tabindex="0" data-sse-id=${s.sseId}>${PageView()}</main>
      ${CommandBar()}
    </div>
    ${BottomTabs()}
    ${DialogHost()}
    ${TerminalPage()}
    ${Toasts()}
  </div>`;
}
