package monitor

// Наблюдение моделей (v2 S7/S8): checkModels → GET /v1/models → список
// моделей в наблюдателе; не-200/не-JSON → без вызова.

import "testing"

type obsFunc func(name string, models []string)

func (f obsFunc) OnModels(name string, models []string) { f(name, models) }

func TestCheckModelsCallsObserver(t *testing.T) {
	h := newMHarn(t, 2)
	h.fetch.modelsStatus = 200
	h.fetch.modelsBody = `{"data":[{"id":"m1"},{"id":"m2"}]}`
	var gotName string
	var gotModels []string
	h.mon.SetModelObserver(obsFunc(func(name string, models []string) {
		gotName, gotModels = name, models
	}))

	h.mon.checkModels("s")
	if gotName != "s" || len(gotModels) != 2 || gotModels[0] != "m1" || gotModels[1] != "m2" {
		t.Fatalf("observer: name=%s models=%v, хочу s [m1 m2]", gotName, gotModels)
	}

	// не-200 → наблюдатель не вызывается.
	h.fetch.modelsStatus = 500
	gotModels = nil
	h.mon.checkModels("s")
	if gotModels != nil {
		t.Fatalf("не-200: наблюдатель вызван (models=%v)", gotModels)
	}

	// не-JSON → наблюдатель не вызывается.
	h.fetch.modelsStatus = 200
	h.fetch.modelsBody = "not json"
	h.mon.checkModels("s")
	if gotModels != nil {
		t.Fatalf("не-JSON: наблюдатель вызван (models=%v)", gotModels)
	}
}
