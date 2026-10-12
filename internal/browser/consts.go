// Package browser — константы (таблица R2): периоды, права, пути.
package browser

import "time"

const (
	// dirPerm — права каталога профиля Chrome (ТЗ Relay 8.1.2): 0700.
	dirPerm = 0o700
	// watchInterval — период опроса сторожа (ТЗ Relay 8.1.5).
	watchInterval = time.Second
	// xvfbWaitMax / xvfbPoll — ожидание готовности Xvfb.
	xvfbWaitMax = 10 * time.Second
	xvfbPoll    = 100 * time.Millisecond
	// restartBackoff — задержка перезапуска (ТЗ Relay 8.1.5): 1 → 30 с, ×2.
	restartBackoffBase = time.Second
	restartBackoffMax  = 30 * time.Second
	restartBackoffStep = 2
	// chromePollTimeout — таймаут проверки живости Chrome (DevTools HTTP).
	chromePollTimeout = 500 * time.Millisecond
	// x11UnixDir — каталог X11-сокетов (проверка готовности Xvfb).
	x11UnixDir = "/tmp/.X11-unix"
	// devtoolsSuffix — суффикс control URL go-rod (вырезаем для DevTools-базы).
	devtoolsSuffix = "/devtools/"
	// versionPath — DevTools-эндпоинт версии (проверка живости Chrome).
	versionPath = "/json/version"
	// viewStopTimeout — максимум ожидания выхода event-цикла screencast
	// при остановке (на статичной странице кадр может не прийти).
	viewStopTimeout = 2 * time.Second
	// URL-схемы DevTools (конвертация ws:// → http:// в devtoolsBase).
	wssScheme   = "wss://"
	wsScheme    = "ws://"
	httpsScheme = "https://"
	httpScheme  = "http://"
	// tildePrefixLen — длина префикса «~/» (expandTilde).
	tildePrefixLen = 2
	// minScreenParts — минимум частей «ШxВxБит» (windowSize).
	minScreenParts = 2
	// p95Quantile — квантиль задержки кадра (CONTROL R1: p95 ≤ 300 мс).
	p95Quantile = 0.95
	// loopbackHost — DevTools слушает только loopback (CONTROL R1).
	loopbackHost = "127.0.0.1"
)
