package notify

import (
	"context"
	"errors"

	"runpilot/internal/command"
)

// Telegram — диспетчер команд Telegram (раздел 12): строка «/…» →
// command.Parse + command.Run через общий Executor координатора (тот же, что
// веб). Result — command.Outcome: Text — ответом в чат, Code — для подписи.
// Разбор не дублируется — делегирован internal/command.
type Telegram struct {
	Exec command.Executor
}

// Run — строка «/…» → Outcome (один и тот же, что даёт веб на той же строке).
// Ошибки разбора → кодовый Outcome: UNKNOWN_COMMAND / BAD_COMMAND (как веб).
func (t *Telegram) Run(ctx context.Context, line string) (command.Outcome, error) {
	p, err := command.Parse(line)
	if err != nil {
		var uk *command.UnknownCommand
		if errors.As(err, &uk) {
			return command.Outcome{Code: "UNKNOWN_COMMAND", Text: err.Error()}, nil
		}
		return command.Outcome{Code: "BAD_COMMAND", Text: err.Error()}, nil
	}
	return command.Run(ctx, p, t.Exec)
}
