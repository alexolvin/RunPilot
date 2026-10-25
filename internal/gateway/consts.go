// Package gateway — константы (таблица R2): лимиты чтения тела отказа,
// метрики.
package gateway

const (
	// bearerPrefix — префикс заголовка Authorization API-клиента.
	bearerPrefix = "bearer "

	// faultBodyLimit — сколько байт тела ответа читается для классификации
	// отказа (S4/S5/S6): шаблоны лежат в начале ошибки vLLM, весь стрим
	// не буферизуется.
	faultBodyLimit = 4096

	// sseEventSep — разделитель событий SSE ("\n\n"): полный блок
	// "data: …\n\n". Используется стриминговым writer'ом API-клиентов.
	sseEventSep = "\n\n"

	// metricLabelParts — частей в ключе метрики "server|code".
	metricLabelParts = 2
	// msToSec — мс → с.
	msToSec = 1000.0

	// Бакеты histogram (раздел 14 ТЗ).
	bucket001  = 0.01
	bucket005  = 0.05
	bucket01   = 0.1
	bucket05   = 0.5
	bucket1    = 1.0
	bucket5    = 5.0
	bucket10   = 10.0
	bucket30   = 30.0
	bucket60   = 60.0
	bucket300  = 300.0
	bucket600  = 600.0
	bucket3600 = 3600.0

	// Бакеты гистограммы размера тела запроса (W15 этап 1), байт.
	bodyBucket1KB   = 1024
	bodyBucket10KB  = 10240
	bodyBucket100KB = 102400
	bodyBucket1MB   = 1048576
	bodyBucket10MB  = 10485760
	bodyBucket100MB = 104857600
	bodyBucket128MB = 134217728
)
