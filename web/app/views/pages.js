// PageView — контент текущей страницы (W4: реальные экраны). Переключается по
// store.page (hash-роут). Каждая страница — отдельный компонент (views/).
import { html } from '../html.js';
import { useStore } from '../store.js';
import { QueuePage } from './queue.js';
import { SessionsPage } from './sessions.js';
import { SessionPage } from './session.js';
import { ServersPage } from './servers.js';
import { NodesPage } from './nodes.js';
import { JournalPage } from './journal.js';
import { MonitoringPage } from './monitoring.js';
import { NotificationsPage } from './notifications.js';
import { SettingsPage } from './settings.js';

// SessionsRoute — список (pageParam пуст) или карточка (pageParam = sid).
// Отдельный компонент: выбор страницы не меняет список хуков PageView (W4).
function SessionsRoute() {
  const s = useStore();
  return s.pageParam ? html`<${SessionPage} />` : html`<${SessionsPage} />`;
}

export function PageView() {
  const s = useStore();
  // Каждая страница — отдельный КОМПОНЕНТ (а не вызов функции): у каждого
  // компонента свой список состояния хуков (useState/useEffect). Вызов как
  // функции `Page()` инлайнит хуки страницы в список PageView и рассинхронизует
  // их при ре-рендере (useState вернёт чужое значение — баг W4).
  switch (s.page) {
    case 'sessions': return html`<${SessionsRoute} />`;
    case 'servers': return html`<${ServersPage} />`;
    case 'nodes': return html`<${NodesPage} />`;
    case 'journal': return html`<${JournalPage} />`;
    case 'monitoring': return html`<${MonitoringPage} />`;
    case 'notifications': return html`<${NotificationsPage} />`;
    case 'settings': return html`<${SettingsPage} />`;
    default: return html`<${QueuePage} />`;
  }
}
