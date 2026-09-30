// Package web — константы (таблица R2): размеры токенов, TTL, отображение.
package web

const (
	// hoursPerDay — часов в сутках (TTL сессии в днях → в часах).
	hoursPerDay = 24
	// randTokenBytes — размер случайного токена (id/csrf) в байтах.
	randTokenBytes = 32
	// actorHashLen — сколько символов id_hash показывать как actor.
	actorHashLen = 8
	// loginPath — маршрут страницы входа (цель редиректа O4).
	loginPath = "/web/login"
)
