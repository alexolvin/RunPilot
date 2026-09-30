// Command protocheck — приёмочная проверка координатора (Э2):
// подключение к каналу узла, hello с заданной версией proto,
// печать кода закрытия соединения. Для неизвестной мажорной версии
// координатор обязан закрыть соединение (раздел 13 ТЗ).
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"runpilot/internal/proto"

	"github.com/gorilla/websocket"
)

func main() {
	var (
		url   = flag.String("url", "ws://127.0.0.1:8788/api/v1/node", "ws-адрес канала узла")
		p     = flag.Int("proto", 999, "версия proto в hello")
		token = flag.String("token", "", "Bearer-токен (пусто — без авторизации)")
	)
	flag.Parse()

	dialer := websocket.Dialer{}
	hdr := http.Header{}
	if *token != "" {
		hdr.Set("Authorization", "Bearer "+*token)
	}
	conn, _, err := dialer.Dial(*url, hdr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protocheck: dial:", err)
		os.Exit(2)
	}
	defer conn.Close()

	m := proto.New(proto.KindHello)
	m.Proto = *p
	m.Host, m.HostIP = "protocheck", "127.0.0.1"
	if err := conn.WriteJSON(m); err != nil {
		fmt.Fprintln(os.Stderr, "protocheck: hello:", err)
		os.Exit(2)
	}

	// Ожидаем close (координатор закрывает при неизвестной версии).
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var closeCode int = -1
	var reason string
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			var cerr *websocket.CloseError
			if errors.As(err, &cerr) {
				closeCode, reason = cerr.Code, cerr.Text
				break
			}
			fmt.Fprintln(os.Stderr, "protocheck: ожидание close:", err)
			os.Exit(3)
		}
		if len(data) >= 2 {
			closeCode = int(data[0])<<8 | int(data[1])
			if len(data) > 2 {
				reason = string(data[2:])
			}
			break
		}
	}
	fmt.Printf("close_code=%d reason=%q proto_sent=%d\n", closeCode, reason, *p)
	if closeCode == websocket.CloseProtocolError {
		fmt.Println("OK: соединение закрыто с протокольной ошибкой")
		return
	}
	fmt.Println("NOT OK: ожидалось закрытие с кодом протокольной ошибки")
	os.Exit(1)
}
