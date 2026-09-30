package gateway

// Наблюдение моделей (v2 разделы S7/S8): монитор передаёт список доступных
// моделей (GET /v1/models); здесь — сравнение с настроенной:
//   - S7: model: auto сменилась → SERVER_MODEL_CHANGED;
//   - S7: заданная модель не найдена → MODEL_PROBLEM;
//   - S8: несколько моделей при model: auto → MODEL_PROBLEM.
//
// Состояние: только переход В MODEL_PROBLEM и ИЗ него (в UP) — UP/DOWN
// остаётся за health-монитором.

import (
	"encoding/json"
	"strings"
	"sync"

	"runpilot/internal/clock"
	"runpilot/internal/model"
)

// ModelWatch — monitor.ModelObserver: сравнение доступных моделей с
// настроенной (раздел S7/S8 ТЗ).
type ModelWatch struct {
	g   *Gateway
	clk clock.Clock

	mu   sync.Mutex
	last map[string]string // name → последняя известная модель (model: auto)
}

// NewModelWatch — наблюдатель моделей (шлюз).
func NewModelWatch(g *Gateway, clk clock.Clock) *ModelWatch {
	return &ModelWatch{g: g, clk: clk, last: map[string]string{}}
}

// OnModels — список моделей с сервера (монитор). Пустой список — без действия.
func (w *ModelWatch) OnModels(name string, models []string) {
	if len(models) == 0 {
		return
	}
	srv := w.g.Servers().ByName(name)
	if srv == nil {
		return
	}
	cfgModel := srv.Cfg.Upstreams.OpenAI.Model

	problem := ""
	changed, from, to := false, "", ""
	if cfgModel == "" || cfgModel == "auto" {
		if len(models) > 1 {
			// S8: несколько моделей при model: auto.
			problem = "несколько моделей при model: auto: " + strings.Join(models, ", ")
		} else {
			cur := models[0]
			w.mu.Lock()
			prev := w.last[name]
			w.last[name] = cur
			w.mu.Unlock()
			if prev != "" && prev != cur {
				changed, from, to = true, prev, cur
			}
		}
	} else {
		found := false
		for _, m := range models {
			if m == cfgModel {
				found = true
				break
			}
		}
		if !found {
			// S7: заданная модель не найдена.
			problem = "модель " + cfgModel + " не найдена, доступна " + models[0]
		}
	}

	cur := w.g.Servers().StateOf(name)
	if problem != "" && cur != model.ServerModelProblem {
		w.g.Servers().SetState(name, model.ServerModelProblem)
		w.emit(name, map[string]any{"problem": true, "reason": problem})
		w.g.log.Warn("runpilot: [WARN] сервер " + name + ": модель — " + problem)
	} else if problem == "" && cur == model.ServerModelProblem {
		w.g.Servers().SetState(name, model.ServerUp)
	}
	if changed {
		// S7: модель сменилась.
		w.emit(name, map[string]any{"changed": true, "from": from, "to": to})
		w.g.log.Warn("runpilot: [WARN] сервер " + name + ": модель сменилась: " + from + " → " + to)
	}
}

// emit — событие SERVER_MODEL в журнал (БД) + телем.
func (w *ModelWatch) emit(name string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_ = w.g.st.EventRecord(model.Event{TS: w.clk.Now(), Kind: model.KindServerModel, Server: name, Payload: b})
}
