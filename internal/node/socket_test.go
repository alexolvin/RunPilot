package node

// socketFor (реестр unmanaged, 8.3 X1) и Prefill (первичный скан до стартового
// KindPanes): unit-тесты.

import (
	"context"
	"testing"
)

// TestSocketForManaged — managed-панель в снапшоте → сокет из snap.
func TestSocketForManaged(t *testing.T) {
	ex := &fakeExec{respond: scanRespond("", "", "")}
	n := newTestNode(t, ex)
	seedPane(t, n, "%10", "sock1")
	got, ok := n.socketFor("%10")
	if !ok || got != "sock1" {
		t.Fatalf("socketFor(%%10) = %q,%v, хочу sock1,true", got, ok)
	}
}

// TestSocketForUnmanaged — unmanaged-панель (только в реестре, НЕ в snap) →
// сокет из реестра. До фикса socketFor читал только snap → adopt всегда
// PANE_GONE (сокет unmanaged-панели не находился).
func TestSocketForUnmanaged(t *testing.T) {
	ex := &fakeExec{respond: scanRespond("", "", "")}
	n := newTestNode(t, ex)
	seedUnmanaged(t, n, PaneInfo{PaneID: "%20"}) // сокет «default» — из реестра
	got, ok := n.socketFor("%20")
	if !ok || got != "default" {
		t.Fatalf("socketFor(%%20) = %q,%v, хочу default,true (реестр unmanaged)", got, ok)
	}
	if _, ok := n.socketFor("%99"); ok {
		t.Error("socketFor(%%99): панели нет — хочу false")
	}
}

// TestPrefillPopulatesPaneList — свежий узел (цикл не отработал) шлёт ПУСТОЙ
// «полный список панелей» → координатор сверкой с БД ложно гонит живые сессии
// в GONE (рестарт терял сессии). Prefill сканирует ДО стартового списка.
func TestPrefillPopulatesPaneList(t *testing.T) {
	const paneID = "%40"
	psOut := "1\t0\t0\t999999\t/sbin/init\n5100\t1\t1000\t50\tqwen\n"
	paneOut := paneLine("s1", paneID, 5100, "qwen", 0, "S1", "", "/p1")
	ex := &fakeExec{respond: scanRespond(psOut, paneOut, idleScreen)}
	n := newTestNode(t, ex)

	if got := len(n.PaneList()); got != 0 {
		t.Fatalf("до Prefill: PaneList = %d, хочу 0 (цикл не отработал)", got)
	}
	if err := n.Prefill(context.Background()); err != nil {
		t.Fatal(err)
	}
	list := n.PaneList()
	if len(list) != 1 {
		t.Fatalf("после Prefill: PaneList = %d, хочу 1", len(list))
	}
	if list[0].PaneID != paneID || list[0].SID != "S1" {
		t.Fatalf("PaneList = %s/%s, хочу %s/S1", list[0].PaneID, list[0].SID, paneID)
	}
}
