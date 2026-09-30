// Package notify — уведомления и Telegram (v2 раздел 12/17).
//
// Разбор и выполнение команд Telegram — тот же, что у веба: общий пакет
// internal/command (Parse + Run + Complete) и общий Executor координатора
// (раздел 12). Пакет не дублирует разбор — он делегирует его command.
//
// Bot API long polling, события и кнопки, coalesce, фильтр chat_ids и /peek
// в разрешённых чатах реализуются на этапе Э7.
package notify
