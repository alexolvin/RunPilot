// Воспроизведение «команды для запуска» (раздел постановки в очередь): заряд
// (текст + токены клавиш) из launchcmd воспроизводится узлом атомарно в момент
// dispatch, а не нажатием прямо в терминал. Текст вставляется побайто
// (paste-buffer, как /paste), клавиши — send-keys. Клавиши по построению из
// allowlist'а: launchcmd.Parse выдаёт только известные токены.
package node

import (
	"context"

	"runpilot/internal/launchcmd"
)

// replayLaunch — выполнить «команду для запуска» на панели по порядку токенов.
// Текст — paste-buffer (load-buffer stdin + paste-buffer -p -d), клавиши —
// tmux send-keys. Возвращает ошибку только при сбое tmux-вызова.
func (c *CommandRunner) replayLaunch(ctx context.Context, socket, paneID, cmd string) error {
	for _, t := range launchcmd.Parse(cmd) {
		switch t.Kind {
		case launchcmd.Text:
			if t.Value == "" {
				continue
			}
			if err := c.pasteText(ctx, socket, paneID, t.Value); err != nil {
				return err
			}
		case launchcmd.Key:
			if err := c.sendLiteral(ctx, socket, paneID, t.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// pasteText — побайтовая вставка текста в панель (load-buffer + paste-buffer
// -p -d). Имя буфера уникально по панели (одновременно воспроизводится одна
// команда на панель, буфер переиспользуется между запусками).
func (c *CommandRunner) pasteText(ctx context.Context, socket, paneID, text string) error {
	buf := "runpilot-launch-" + paneID
	if _, err := c.ex.OutputInput(ctx, "tmux", text, tmuxArgs(socket, "load-buffer", "-b", buf, "-")...); err != nil {
		return err
	}
	_, err := c.ex.Output(ctx, "tmux", tmuxArgs(socket, "paste-buffer", "-p", "-d", "-b", buf, "-t", paneID)...)
	return err
}
