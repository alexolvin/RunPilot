// Package command — константы (R2): позиции аргументов.
package command

const (
	// two — «вторая позиция после имени команды»: индекс первого флага у
	// /new и база позиции введённого аргумента в подсказках.
	two = 2

	// CompleteBudgetMS — бюджет подсказок p95 (ui.complete_budget_ms, раздел 12).
	// Зеркалирует web/app/constants.js (UI.completeBudgetMs); бэкенд обязан
	// укладываться, фронт ждёт ответ не дольше этого значения.
	CompleteBudgetMS = 80
)
