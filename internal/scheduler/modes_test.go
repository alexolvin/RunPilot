package scheduler

// Режимы безопасности (v2 разделы 6.2/6.4): аварийная остановка и
// безопасный режим. CONTROL 3/4.

import (
	"errors"
	"testing"
	"time"

	"runpilot/internal/model"
	"runpilot/internal/proto"
)

// CONTROL 3 (6.2): аварийная остановка — идущий ход снят (аренда EMERGENCY,
// сессия HOLD(EMERGENCY)), новые аренды не выдаются, meta.mode=EMERGENCY
// (переживает рестарт), выход → PAUSED.
func TestEmergencyStop(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.grantPending("s1")
	h.sch.InjectReply("s1", proto.Msg{Result: proto.ResAlreadyBusy}, nil)
	if st := h.sessionState("s1"); st != model.SessionRunning {
		t.Fatalf("предпосылка: state=%s, хочу RUNNING", st)
	}

	if err := h.sch.SetEmergency(); err != nil {
		t.Fatal(err)
	}
	if got := h.sch.Mode(); got != string(model.ModeEmergency) {
		t.Fatalf("mode=%s, хочу EMERGENCY", got)
	}
	if st := h.sessionState("s1"); st != model.SessionHold {
		t.Fatalf("после emergency: state=%s, хочу HOLD", st)
	}
	if hr := h.session("s1").HoldReason; hr != model.HoldEmergency {
		t.Fatalf("hold_reason=%s, хочу EMERGENCY", hr)
	}
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("после emergency: аренда не снята")
	}
	if m, _ := h.st.Mode(); m != string(model.ModeEmergency) {
		t.Fatalf("meta.mode=%q, хочу EMERGENCY (переживает рестарт)", m)
	}

	// Новые аренды не выдаются.
	h.addSession("s2", "s2", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s2", "host")
	h.enqueue("s2", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s2"); ok {
		t.Fatal("EMERGENCY: аренда выдана, новых аренд быть не должно")
	}

	// Выход → PAUSED; сессия HOLD(EMERGENCY) возвращается в очередь.
	if err := h.sch.ClearEmergency(); err != nil {
		t.Fatal(err)
	}
	if got := h.sch.Mode(); got != string(model.ModePaused) {
		t.Fatalf("после снятия: mode=%s, хочу PAUSED", got)
	}
	if n := h.sch.RequeueHoldEmergency(); n != 1 {
		t.Fatalf("requeue emergency: %d, хочу 1", n)
	}
	if st := h.sessionState("s1"); st != model.SessionQueued {
		t.Fatalf("после requeue: state=%s, хочу QUEUED", st)
	}
}

// CONTROL 4 (6.4): безопасный режим — новые аренды не выдаются; после
// успешной пробной записи (≥ safe_probe_sec) режим снят и аренда выдаётся.
func TestSafeModeNoGrants(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	if err := h.sch.SetSafeMode("SQLITE_FULL"); err != nil {
		t.Fatal(err)
	}
	if got := h.sch.Mode(); got != string(model.ModeSafeMode) {
		t.Fatalf("mode=%s, хочу SAFE_MODE", got)
	}

	// Новая сессия в SAFE_MODE: аренда НЕ выдаётся.
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if l, ok := h.leaseOf("s1"); ok {
		t.Fatalf("SAFE_MODE: аренда выдана (%s), новых аренд быть не должно", l.State)
	}

	// Устранение ошибки: пробная запись успешна → выход за safe_probe_sec.
	probe := time.Duration(h.cfg.Monitor.SafeProbeSec) * time.Second
	h.advance(probe + 2*time.Second)
	h.tick()
	if got := h.sch.Mode(); got != string(model.ModeNormal) {
		t.Fatalf("после safe_probe: mode=%s, хочу NORMAL", got)
	}

	// Теперь аренда выдаётся.
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); !ok {
		t.Fatal("после выхода SAFE_MODE: аренда не выдана")
	}
}

// CONTROL 4 (6.4): пока ошибка записи не устранена — безопасный режим
// сохраняется (пробная запись не успешна).
func TestSafeModePersistsOnWriteError(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	if err := h.sch.SetSafeMode("SQLITE_FULL"); err != nil {
		t.Fatal(err)
	}
	// Замыкаем store: пробная запись не может быть успешной.
	if err := h.st.Close(); err == nil {
		// Open заново не нужно — ProbeWrite вернёт ErrClosed.
	}
	probe := time.Duration(h.cfg.Monitor.SafeProbeSec) * time.Second
	h.advance(probe + 2*time.Second)
	h.tick()
	if got := h.sch.Mode(); got != string(model.ModeSafeMode) {
		t.Fatalf("с ошибкой записи: mode=%s, хочу SAFE_MODE (режим сохраняется)", got)
	}
}

// CONTROL 4 (K6): внедрённая ошибка записи в БД автоматически переводит
// координатор в SAFE_MODE; новые аренды не выдаются.
func TestWriteErrorEntersSafeMode(t *testing.T) {
	h := newHarn(t, []ServerView{srv("s", 10, 1)})
	h.sch.onStoreWriteError(errors.New("SQLITE_FULL: no free space"))
	if got := h.sch.Mode(); got != string(model.ModeSafeMode) {
		t.Fatalf("после ошибки записи: mode=%s, хочу SAFE_MODE", got)
	}
	h.addSession("s1", "s1", "host", model.SessionQueued, model.ClassNormal, model.NoConstraint)
	h.idlePane("s1", "host")
	h.enqueue("s1", model.ClassNormal, model.NoConstraint, model.QueueModeSubmit)
	h.advance(3 * time.Second)
	h.tick()
	if _, ok := h.leaseOf("s1"); ok {
		t.Fatal("SAFE_MODE (write error): аренда выдана, новых аренд быть не должно")
	}
	// Повторная ошибка — идемпотентно (режим не меняется, не дублируется).
	h.sch.onStoreWriteError(errors.New("SQLITE_FULL"))
	if got := h.sch.Mode(); got != string(model.ModeSafeMode) {
		t.Fatalf("после 2-й ошибки: mode=%s, хочу SAFE_MODE", got)
	}
}
