// Числа поведения фронтенда (v2 раздел 18: ui.* + точки перелома 10.1).
// EДИНСТВЕННОЕ место числовых литералов web/ (R2: lintweb — числа в JS только
// здесь, кроме 0/1/-1). CSS-размеры живут в web/styles/tokens.css.

// ui.* — поведение (разделы 12, 19, TW10, TW11).
export const UI = {
  // История командной строки при пустом вводе (12: ui.cmd_history_max).
  cmdHistoryMax: 50,
  // Показ скелетона при загрузке дольше (9.3: ui.skeleton_delay_ms).
  skeletonDelayMs: 150,
  // Бюджет подсказок команд p95 (12: ui.complete_budget_ms).
  completeBudgetMs: 80,
  // Задержка перед запросом подсказок (дебаунс), мс.
  completeDebounceMs: 120,
  // Максимум строк композера карточки (13.1: ui.composer_max_lines).
  composerMaxLines: 8,
  // Видок терминала в браузере на десктопе (13.4: ui.terminal_viewport_pct):
  // доля экрана (0–100), которую занимает окно оверлея; телефон — весь экран.
  terminalViewportPct: 85,
  // Терминал: размер шрифта (px) и история строк xterm.js (13.4).
  termFontSizePx: 13,
  // Inline-терминал карточки сессии (доп-3i): шрифт меньше, чтобы не
  // выделялся на фоне текста страницы (страница = 14 px).
  termInlineFontSizePx: 12,
  termScrollback: 2000,
  // Терминал: сколько показывать фидбек «Скопировано» (мс).
  termCopyFlashMs: 1500,
  // Баннер «Нет связи» не позже (TW10: ui.offline_banner_ms).
  offlineBannerMs: 250,
  // Максимум видимых подсказок команд.
  maxSuggestions: 8,
  // Периодический опрос /meta (v2 15.4: перечисления, единицы, версия).
  metaPollMs: 30000,
  // Размер страницы журнала (визуальная подгрузка; серверный лимит — web.page_size).
  journalPageSize: 50,
  // Максимум строк «В работе» на экране очереди (остальное — «показать ещё»).
  queueWorkMax: 20,
  // Максимум событий на странице журнала (защита от огромной подгрузки).
  journalRowsMax: 200,
  // Тосты: сколько держать в списке и сколько их жить (мс).
  toastMax: 5,
  toastTtlMs: 4000,
  // Тост с действием (O5/J12 «Отменить») живёт дольше окна web.undo_sec.
  toastActionTtlMs: 30000,
};

// Точки перелома (10.1): значения зеркалируют комментарии tokens.css —
// JS не видит CSS-медиазапросы, поэтому дублируются здесь.
export const BP = {
  wide: 1280, // >= широкая боковая панель 240 px
  narrow: 1024, // 1024–1279 узкая 72 px
  tablet: 768, // 768–1023 планшет; < 768 телефон
};

// Минимальный размер интерактивного элемента на телефоне (19.3.6: 44×44 px).
export const TOUCH_MIN_PX = 44;

// HTTP-коды (W1/W6): неверный токен, лимит попыток, «принято» (удаляется).
export const HTTP = {
  unauthorized: 401,
  rateLimited: 429,
  accepted: 202,
};

// W6 (5.1/11.5/11.10): диалоги, spawn, автодополнение, ревизии.
export const W6 = {
  nameCheckMinLen: 2,        // минимум символов имени для name-check
  nameCheckDebounceMs: 250,  // дебаунс name-check
  dirDebounceMs: 200,        // дебаунс автодополнения каталогов
  spawnOkDelayMs: 600,       // пауза до закрытия после успешного spawn
  copyFlashMs: 1500,         // «скопировано» в диалоге подключения
  revPreviewLen: 80,         // превью diff ревизии
  defaultPriority: 10,       // приоритет нового сервера (мастер)
};

// Число серверных цветов (tokens.css: --server-1..--server-5).
export const SERVER_COLORS = 5;

// Факторы форматирования (R2: числа только здесь).
export const FMT = {
  secPerMin: 60,
  secPerHour: 3600,
  msPerSec: 1000,
  doubleDigit: 10,
  // Значения метрик — не более двух знаков после точки (телефон: не вылезают
  // из фрейма).
  round2Digits: 2,
};

// Спарклайны (viewBox; ширина — 100% контейнера, высота фиксирована в CSS).
export const CHART = {
  w: 100,
  h: 30,
  minPoints: 2,
  mid: 0.5,
};
