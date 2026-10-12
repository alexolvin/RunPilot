// Command runpilot-fakeorch — тестовый двойник сайта оркестратора (ТЗ Relay §17).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"runpilot/internal/testutil/fakeorch"
)

func main() {
	port := flag.Int("port", defPort, "порт")
	flag.Parse()
	s := fakeorch.New()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf(startMsg, addr)
	log.Fatal(http.ListenAndServe(addr, s.Handler()))
}
