// runpilot-fakellm — тестовый двойник vLLM (раздел 15 ТЗ) для живых стендов
// и ручных проверок: /health, /v1/models, /metrics (формат vLLM),
// /v1/chat/completions (SSE) + управляемые running/waiting.
//
// Отличия от fakellm в тестах — контрольные эндпоинты /_set-metrics,
// /_set-delays и /_set-fail (в тестах есть прямой Go API).
//
// Флаги:
//
//	-port N         порт (по умолчанию 8801), bind 127.0.0.1
//	-model M        имя модели (по умолчанию fake-e6)
//	-models-file F  тело /v1/models (снимок реального vLLM); пусто — стандартный
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"runpilot/internal/testutil/fakellm"
)

func main() {
	port := flag.Int("port", defaultPort, "порт (bind 127.0.0.1)")
	model := flag.String("model", "fake-e6", "имя модели")
	modelsFile := flag.String("models-file", "", "файл с телом /v1/models (снимок реального vLLM); пусто — стандартный ответ")
	flag.Parse()

	f := fakellm.New(*model)
	if *modelsFile != "" {
		b, err := os.ReadFile(*modelsFile)
		if err != nil {
			log.Fatalf("runpilot-fakellm: models-file: %v", err)
		}
		f.SetModelsBody(b)
	}
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
	// Контрольный эндпоинт стенда: /_set-delays?firstByteMs=&chunkPauseMs=&chunks=.
	// Задержки фиксируются на запрос в момент его приёма; смена настроек на
	// уже идущие запросы не влияет (SetDelays читается в serveCompletion).
	mux.HandleFunc("/_set-delays", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		firstMS, _ := strconv.ParseInt(q.Get("firstByteMs"), base10, intBitSize)
		chunkMS, _ := strconv.ParseInt(q.Get("chunkPauseMs"), base10, intBitSize)
		chunks, _ := strconv.Atoi(q.Get("chunks"))
		f.SetDelays(time.Duration(firstMS)*time.Millisecond, time.Duration(chunkMS)*time.Millisecond, chunks)
		fmt.Fprintf(w, "ok firstByteMs=%d chunkPauseMs=%d chunks=%d\n", firstMS, chunkMS, chunks)
	})
	// Контрольный эндпоинт стенда: /_set-fail?n=&mode=. mode: "" (выключить),
	// "502"/"503"/"504"/"conn" — отказ до первого байта, "post" — после.
	mux.HandleFunc("/_set-fail", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		n, _ := strconv.Atoi(q.Get("n"))
		mode := q.Get("mode")
		if mode == "post" {
			f.SetFailAfter(n)
		} else {
			f.SetFailBefore(n, mode)
		}
		fmt.Fprintf(w, "ok n=%d mode=%q\n", n, mode)
	})
	mux.Handle("/", f.Handler())

	addr := "127.0.0.1:" + strconv.Itoa(*port)
	log.Printf("runpilot-fakellm: модель=%q listen=%s", *model, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
