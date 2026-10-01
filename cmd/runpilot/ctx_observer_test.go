package main

// item 3: ctxObserver — min размера контекста по серверам (monitor →
// координатор → qwen contextWindowSize).

import "testing"

func TestCtxObserverMinEmpty(t *testing.T) {
	o := &ctxObserver{}
	if got := o.Min(); got != 0 {
		t.Fatalf("пустой: Min()=%d, хочу 0", got)
	}
}

func TestCtxObserverMinSingleAndMultiple(t *testing.T) {
	o := &ctxObserver{}
	o.OnModelContext("a", 262144)
	if got := o.Min(); got != 262144 {
		t.Fatalf("один сервер: Min()=%d, хочу 262144", got)
	}
	// Второй сервер с БОЛЬШЕ контекстом — min не меняется.
	o.OnModelContext("b", 524288)
	if got := o.Min(); got != 262144 {
		t.Fatalf("a=262144,b=524288: Min()=%d, хочу 262144", got)
	}
	// Второй сервер с МЕНЬШЕ контекстом — min опускается.
	o.OnModelContext("c", 131072)
	if got := o.Min(); got != 131072 {
		t.Fatalf("a,b,c: Min()=%d, хочу 131072 (меньший)", got)
	}
}

func TestCtxObserverUpdateAndIgnore(t *testing.T) {
	o := &ctxObserver{}
	o.OnModelContext("a", 131072)
	// Обновление того же сервера — последнее значение.
	o.OnModelContext("a", 262144)
	if got := o.Min(); got != 262144 {
		t.Fatalf("обновление a: Min()=%d, хочу 262144", got)
	}
	// <= 0 — игнорируется.
	o.OnModelContext("a", 0)
	o.OnModelContext("a", -1)
	if got := o.Min(); got != 262144 {
		t.Fatalf("после <=0: Min()=%d, хочу 262144 (не затёрто)", got)
	}
}
