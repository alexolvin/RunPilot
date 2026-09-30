package gateway

// Наблюдение моделей (v2 S7/S8): сравнение доступных моделей с настроенной →
// SERVER_MODEL_CHANGED / MODEL_PROBLEM (состояние сервера + событие).

import (
	"testing"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/clock"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

func autoSrvCfg(name, url string) config.Server {
	c := srvCfg(name, 10, 1, url, "RUNPILOT_TEST_KEY")
	c.Upstreams.OpenAI.Model = "auto"
	return c
}

// S7/S8: переходы состояния сервера по наблюдаемым моделям.
func TestModelWatchStates(t *testing.T) {
	t.Setenv("RUNPILOT_TEST_KEY", "k")
	a := fakellm.Start("srv-model")
	defer a.Close()
	st, gw, _ := testEnv(t, []config.Server{
		srvCfg("spec", 10, 1, a.URL, "RUNPILOT_TEST_KEY"), // model = "srv-model"
		autoSrvCfg("auto", a.URL),                      // model = "auto"
	})
	w := NewModelWatch(gw, clock.NewReal())

	// S7: заданная модель найдена → UP.
	w.OnModels("spec", []string{"srv-model"})
	if s := gw.Servers().StateOf("spec"); s != model.ServerUp {
		t.Fatalf("spec found: state=%s, хочу UP", s)
	}
	// S7: заданная модель НЕ найдена → MODEL_PROBLEM.
	w.OnModels("spec", []string{"other"})
	if s := gw.Servers().StateOf("spec"); s != model.ServerModelProblem {
		t.Fatalf("spec notfound: state=%s, хочу MODEL_PROBLEM", s)
	}
	// S7: снова найдена → UP (снять MODEL_PROBLEM).
	w.OnModels("spec", []string{"srv-model"})
	if s := gw.Servers().StateOf("spec"); s != model.ServerUp {
		t.Fatalf("spec found again: state=%s, хочу UP", s)
	}
	// S8: model: auto + несколько моделей → MODEL_PROBLEM.
	w.OnModels("auto", []string{"m1", "m2"})
	if s := gw.Servers().StateOf("auto"); s != model.ServerModelProblem {
		t.Fatalf("auto multi: state=%s, хочу MODEL_PROBLEM", s)
	}
	// S8 → одна модель: MODEL_PROBLEM снят → UP.
	w.OnModels("auto", []string{"m1"})
	if s := gw.Servers().StateOf("auto"); s != model.ServerUp {
		t.Fatalf("auto single: state=%s, хочу UP", s)
	}
	// S7: model: auto сменилась (m1 → m2) → событие SERVER_MODEL(changed).
	// Запись события асинхронная (QueueWrite) — опрашиваем с дедлайном.
	w.OnModels("auto", []string{"m2"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		evs, err := st.EventList(0, 50)
		if err != nil {
			t.Fatal(err)
		}
		changed := false
		for _, e := range evs {
			if e.Kind == model.KindServerModel && e.Server == "auto" &&
				len(e.Payload) > 0 && contains(e.Payload, `"changed":true`) {
				changed = true
			}
		}
		if changed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("нет события SERVER_MODEL(changed) для auto")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(b []byte, sub string) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == sub {
			return true
		}
	}
	return false
}
