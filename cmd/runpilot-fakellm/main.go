// Command runpilot-fakellm — fakellm-сервер для стендов (petloop).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"runpilot/internal/testutil/fakellm"
)

func main() {
	port := flag.Int("port", defPort, "порт")
	model := flag.String("model", defModel, "имя модели")
	flag.Parse()

	s := fakellm.New(*model)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf(startMsg, addr, *model)
	log.Fatal(http.ListenAndServe(addr, s.Handler()))
}
