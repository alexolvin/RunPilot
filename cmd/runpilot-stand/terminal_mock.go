package main

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// mockTerminal — W8: терминал-эхо для скриншотов/е2е (runpilot-stand). Реального
// tmux/PTY в стенде нет; воспроизводится протокол 13.4: первым текстовый кадр
// {"type":"open","chan"}, затем стартовый вывод панели (подсказка bash) и
// эхо binary-кадров браузера — xterm.js видит живой ввод и эхо нажатий.
//
// CheckOrigin=true допустим: стенд работает на loopback, проверка Origin =
// web.public_url выполняется в web.handleTerminal ДО вызова этого хендлера.
var mockUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func mockTerminal(w http.ResponseWriter, r *http.Request, sid, actor string) {
	conn, err := mockUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// Открывающий JSON-кадр (13.4: сервер первым шлёт {"type":"open"}).
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"open","chan":1}`)); err != nil {
		return
	}
	// Стартовый вывод: заголовок панели + подсказка (UTF-8, как вывод tmux).
	// Без SGR-bold: xterm рендерит bold чёрным независимо от theme.foreground
	// (контраст 19.3.5 на тёмном фоне), обычный вес берёт светлый foreground.
	banner := "RunPilot · панель " + sid + "\r\n" +
		"bash-5.2$ echo \"терминал в браузере работает\"\r\n" +
		"терминал в браузере работает\r\n" +
		"bash-5.2$ "
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(banner)); err != nil {
		return
	}
	// Эхо: binary-кадр ввода → обратно (xterm показывает нажатие клавиши).
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.BinaryMessage, data)
	}
}
