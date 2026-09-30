// runpilot-fakellm — тестовый двойник vLLM (раздел 15 ТЗ) для живых стендов
// и ручных проверок: /health, /v1/models, /metrics (формат vLLM),
// /v1/chat/completions (SSE) + управляемые running/waiting.
//
// Отличия от fakellm в тестах — только контрольный эндпоинт
// /_set-metrics (в тестах есть прямой Go API SetMetrics).
//
// Флаги:
//
//	-port N    порт (по умолчанию 8801), bind 127.0.0.1
//	-model M   имя модели (по умолчанию fake-e6)
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"runpilot/internal/testutil/fakellm"
)

func main() {
	port := flag.Int("port", defaultPort, "порт (bind 127.0.0.1)")
	model := flag.String("model", "fake-e6", "имя модели")
	flag.Parse()

	f := fakellm.New(*model)
	mux := http.NewServeMux()
	// Контрольный эндпоинт стенда: /_set-metrics?running=N&waiting=N&kv=F.
	mux.HandleFunc("/_set-metrics", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		running, _ := strconv.Atoi(q.Get("running"))
		waiting, _ := strconv.Atoi(q.Get("waiting"))
		kv, _ := strconv.ParseFloat(q.Get("kv"), floatBitSize)
		f.SetMetrics(running, waiting, kv)
		fmt.Fprintf(w, "ok running=%d waiting=%d kv=%g\n", running, waiting, kv)
	})
	mux.Handle("/", f.Handler())

	addr := "127.0.0.1:" + strconv.Itoa(*port)
	log.Printf("runpilot-fakellm: модель=%q listen=%s", *model, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
