package gateway

// TE-<ID> — раздел 7 ТЗ (CONTROL W7). Группа S: реакция шлюза.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"runpilot/internal/clock"
	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// TestTE_S4 — OOM GPU в vLLM: 5xx с шаблоном класса OOM → SERVER_FAULT(OOM)
// (reportFault), ход → ERROR_TRANSIENT → RESUME (отслеживается наблюдателем).
func TestTE_S4(t *testing.T) {
	up := upOom(t, `{"error":"CUDA out of memory. Tried to allocate 2.00 GiB"}`)
	t.Setenv("RUNPILOT_TE_S4_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, up.URL, "RUNPILOT_TE_S4_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)

	ok := mkSession(t, st, model.SessionIdle)
	resp, body := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("статус: %d тело: %s, хочу 500", resp.StatusCode, body)
	}
	if calls := rep.got(); len(calls) != 1 || calls[0] != model.FaultOOM {
		t.Fatalf("SERVER_FAULT: %v, хочу [OOM]", calls)
	}
}

// TestTE_S5 — отказ движка vLLM: 5xx с шаблоном ENGINE_DEAD →
// SERVER_FAULT(ENGINE_DEAD).
func TestTE_S5(t *testing.T) {
	up := upOom(t, `{"detail":"vllm.engine.EngineDeadError: Engine core process died"}`)
	t.Setenv("RUNPILOT_TE_S5_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, up.URL, "RUNPILOT_TE_S5_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)

	ok := mkSession(t, st, model.SessionIdle)
	resp, _ := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("статус: %d, хочу 500", resp.StatusCode)
	}
	if calls := rep.got(); len(calls) != 1 || calls[0] != model.FaultEngineDead {
		t.Fatalf("SERVER_FAULT: %v, хочу [ENGINE_DEAD]", calls)
	}
}

// TestTE_S11 — нет ключа: key_env задан, но переменная пуста в окружении →
// KEY_MISSING (сервер не годится для аренд).
func TestTE_S11(t *testing.T) {
	t.Setenv("RUNPILOT_TE_S11_SET", "k")
	cfg := []config.Server{
		srvCfg("nokey", 10, 1, "http://127.0.0.1:1", "RUNPILOT_TE_S11_UNSET"),
	}
	srvs, err := NewServers(cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st := srvs.StateOf("nokey"); st != model.ServerKeyMissing {
		t.Fatalf("nokey: state=%s, хочу KEY_MISSING", st)
	}
}

// TestTE_S7 — модель исчезла: заданная модель не найдена в /v1/models →
// сервер MODEL_PROBLEM (не годится для новых аренд) + событие SERVER_MODEL.
func TestTE_S7(t *testing.T) {
	t.Setenv("RUNPILOT_TE_S7_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	st, gw, _ := testEnv(t, []config.Server{
		srvCfg("spec", 10, 1, a.URL, "RUNPILOT_TE_S7_KEY"), // model = "srv-model"
	})
	w := NewModelWatch(gw, clock.NewReal())
	// Заданная модель (srv-model) отсутствует — доступна только другая.
	w.OnModels("spec", []string{"other"})
	if s := gw.Servers().StateOf("spec"); s != model.ServerModelProblem {
		t.Fatalf("spec: state=%s, хочу MODEL_PROBLEM (модель не найдена)", s)
	}
	// Событие SERVER_MODEL записано (аудит/телем).
	deadline := time.Now().Add(2 * time.Second)
	for {
		evs, _ := st.EventList(0, 50)
		if len(evs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("нет события SERVER_MODEL при исчезновении модели")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTE_S8 — модель вернулась: заданная модель снова найдена → UP
// (MODEL_PROBLEM снят), новые ходы идут.
func TestTE_S8(t *testing.T) {
	t.Setenv("RUNPILOT_TE_S8_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	_, gw, _ := testEnv(t, []config.Server{
		srvCfg("spec", 10, 1, a.URL, "RUNPILOT_TE_S8_KEY"),
	})
	w := NewModelWatch(gw, clock.NewReal())
	w.OnModels("spec", []string{"other"})        // исчезла → MODEL_PROBLEM
	w.OnModels("spec", []string{"srv-model"})     // вернулась → UP
	if s := gw.Servers().StateOf("spec"); s != model.ServerUp {
		t.Fatalf("spec: state=%s, хочу UP (модель вернулась)", s)
	}
}

// TestTE_U4 — обновился vLLM: после рестарта координатор перечитывает
// /v1/models — модель auto сменилась → SERVER_MODEL(changed) (S7), сервер
// остаётся UP (S1/S2 — здоровье).
func TestTE_U4(t *testing.T) {
	t.Setenv("RUNPILOT_TE_U4_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	autoCfg := srvCfg("auto", 10, 1, a.URL, "RUNPILOT_TE_U4_KEY")
	autoCfg.Upstreams.OpenAI.Model = "auto" // vLLM с auto-моделью
	st, gw, _ := testEnv(t, []config.Server{autoCfg})
	w := NewModelWatch(gw, clock.NewReal())
	w.OnModels("auto", []string{"model-v1"}) // текущая модель
	if s := gw.Servers().StateOf("auto"); s != model.ServerUp {
		t.Fatalf("до обновления: state=%s, хочу UP", s)
	}
	// vLLM обновлён: новая модель со следующего запроса.
	w.OnModels("auto", []string{"model-v2"})
	if s := gw.Servers().StateOf("auto"); s != model.ServerUp {
		t.Fatalf("после обновления: state=%s, хочу UP (новое vLLM годится)", s)
	}
	// Событие SERVER_MODEL(changed).
	deadline := time.Now().Add(2 * time.Second)
	for {
		evs, _ := st.EventList(0, 50)
		found := false
		for _, e := range evs {
			if e.Kind == model.KindServerModel && strings.Contains(string(e.Payload), `"changed":true`) {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("нет события SERVER_MODEL(changed) при обновлении vLLM")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTE_S13 — таймаут до первого байта (first_byte_timeout_sec): апстрим
// держит заголовки дольше таймаута → миграция на другой сервер (ход прошёл
// на втором), отказ транзиторный.
func TestTE_S13(t *testing.T) {
	a := fakellm.Start("srv-a")
	defer a.Close()
	b := fakellm.Start("srv-b")
	defer b.Close()
	t.Setenv("RUNPILOT_TE_S13_KEY_A", "ka")
	t.Setenv("RUNPILOT_TE_S13_KEY_B", "kb")
	cfgA := srvCfg("a", 10, 1, a.URL, "RUNPILOT_TE_S13_KEY_A")
	cfgA.FirstByteTimeoutSec = 1 // короткий таймаут до первого байта
	cfgB := srvCfg("b", 5, 1, b.URL, "RUNPILOT_TE_S13_KEY_B")
	st, _, ts := testEnv(t, []config.Server{cfgA, cfgB})
	a.SetDelays(3*time.Second, 0, 3) // держит первый байт дольше таймаута
	s := mkSession(t, st, model.SessionIdle)
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d, хочу 200 (миграция на b по таймауту)", resp.StatusCode)
	}
	if got := b.Requests(); got != 1 {
		t.Fatalf("запросов к b: %d, хочу 1 (ход с мигрированного a)", got)
	}
	sess, _ := st.GetSession(s)
	if sess.LastMigratedFrom != "a" {
		t.Fatalf("last_migrated_from: %q, хочу a", sess.LastMigratedFrom)
	}
}

// TestTE_S14 — сервер упал в середине потокового ответа: первый байт ушёл,
// потом обрыв. Реакция шлюза: отказа сервера НЕТ (без карантина — это не
// 5xx с шаблоном), миграции НЕТ (после первого байта поздно), сервер не
// выводится из строя; ход в таком случае идёт в ERROR (повтор — на
// планировщике). Здесь проверяется серверная реакция шлюза.
func TestTE_S14(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TE_S14_KEY", "k")
	st, gw, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TE_S14_KEY")})
	rep := &recFault{}
	gw.SetFaultReporter(rep)
	s := mkSession(t, st, model.SessionIdle)
	a.SetDelays(0, 5*time.Millisecond, 5)
	a.SetFailAfter(1) // первый байт уходит, затем обрыв посреди потока
	// Стриминг-запрос: без stream:true обрыва посреди SSE нет.
	_, _ = postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", `"stream":true`))

	// Запрос дошёл до сервера (обрыв был ПОСЛЕ первого байта).
	if got := a.Requests(); got < 1 {
		t.Fatalf("запросов к a: %d, хочу >=1 (сервер обслуживал ход)", got)
	}
	// Миграции нет: после первого байта поздно переключаться.
	sess, _ := st.GetSession(s)
	if sess.LastMigratedFrom != "" {
		t.Fatalf("last_migrated_from: %q, хочу пусто (после первого байта миграции нет)", sess.LastMigratedFrom)
	}
	// Отказа сервера нет (не 5xx с шаблоном) → не в карантине, UP.
	if calls := rep.got(); len(calls) != 0 {
		t.Fatalf("неожиданный отказ сервера: %v (S14 — без отказа)", calls)
	}
	if st2 := gw.Servers().StateOf("a"); st2 == model.ServerDown || st2 == model.ServerQuarantined {
		t.Fatalf("состояние a: %s, хочу не DOWN/QUARANTINED (обрыв потока не валит сервер)", st2)
	}
}

// TestTE_C11 — запрос сессии с чужого IP: шлюз отвечает 403 (раздел 6 ТЗ),
// не чаще раза в notify.telegram.coalesce_sec (уведомление дедуплицируется).
func TestTE_C11(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TE_C11_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TE_C11_KEY")})
	ok := mkSession(t, st, model.SessionIdle)
	// Чужой IP (создатель запроса — 127.0.0.1, host_ip сессии — другой).
	if _, err := st.DB().Exec(`UPDATE session SET host_ip = '10.9.9.9' WHERE sid = ?`, ok); err != nil {
		t.Fatal(err)
	}
	resp, _ := postJSON(t, ts.URL+"/s/"+ok+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой IP: %d, хочу 403", resp.StatusCode)
	}
}

// TestTE_C8 — ручной Enter в tmux: запрос БЕЗ аренды → неявная аренда
// (origin=IMPLICIT, раздел 6 ТЗ правило 3) или удержание; сессия IDLE →
// RUNNING, аренда записана в журнал (БД).
func TestTE_C8(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_TE_C8_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_TE_C8_KEY")})
	s := mkSession(t, st, model.SessionIdle) // аренды нет (ручной Enter)

	resp, body := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("srv-model", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d тело: %s, хочу 200 (неявный захват слота)", resp.StatusCode, body)
	}
	// Неявная аренда создана с origin=IMPLICIT.
	lease, err := st.LeaseGet(s)
	if err != nil {
		t.Fatalf("нет аренды после ручного Enter: %v", err)
	}
	if lease.Origin != model.LeaseOriginImplicit {
		t.Fatalf("origin=%q, хочу IMPLICIT (ручной Enter → неявная аренда)", lease.Origin)
	}
	// Сессия IDLE → RUNNING.
	sess, _ := st.GetSession(s)
	if sess.State != model.SessionRunning {
		t.Fatalf("состояние сессии: %s, хочу RUNNING (ручной Enter со слотом)", sess.State)
	}
}
