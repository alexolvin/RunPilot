// Package monitor — константы (таблица R2): поля GPU, единицы, периоды.
package monitor

const (
	// nvidiaFieldCount — число полей в строке nvidia-smi CSV.
	nvidiaFieldCount = 6
	// gpuField* — позиции полей nvidia-smi CSV (0=index, 1=util).
	gpuFieldMemUsed  = 2
	gpuFieldMemTotal = 3
	gpuFieldTemp     = 4
	gpuFieldPower    = 5
	// floatBitSize — разрядность strconv.ParseFloat.
	floatBitSize = 64
	// kibi — байт в кибибайте (конверсии памяти MiB/GB/B).
	kibi = 1024
	// tickPollMS — период опроса дедлайнов в Run (мс).
	tickPollMS = 100
	// minRateSamples — минимум точек для расчёта Rate.
	minRateSamples = 2
	// percentWhole — умножитель доля → проценты.
	percentWhole = 100
	// decimalBase — основание 10 для strconv (K4: МБ в сообщении).
	decimalBase = 10
)
