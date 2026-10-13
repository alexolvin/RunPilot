package api

// ТЗ «запуск сессий на узле выбранного пользователя»: идентичность узла =
// host:os_user. Два узла с одинаковым host и разными os_user подключаются
// одновременно; spawn — на выбранный узел; имя сессии глобально (NAME_IN_USE).

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"runpilot/internal/proto"
)

// addFakeNodeWithUser — fake-узел с ос_user (идентичность host:os_user).
func addFakeNodeWithUser(t *testing.T, hub *Hub, host, osUser, result, detail string) *proto.Msg {
	t.Helper()
	cap := &proto.Msg{}
	hub.Register(&NodeInfo{
		Host: host, OsUser: osUser, HostIP: "127.0.0.1",
		Conn: &fakeConn{hub: hub, capture: cap, result: result, detail: detail},
	})
	t.Cleanup(func() { hub.Unregister(host + ":" + osUser) })
	return cap
}

// TestNodeIdentityCoexist — два узла с одним host и разными os_user
// сосуществуют в реестре (ключ host:os_user).
func TestNodeIdentityCoexist(t *testing.T) {
	_, _, hub := testServer(t)
	addFakeNodeWithUser(t, hub, "srv1", "alice", proto.ResSpawned, "%0")
	addFakeNodeWithUser(t, hub, "srv1", "bob", proto.ResSpawned, "%0")

	if _, ok := hub.Node("srv1:alice"); !ok {
		t.Error("узел srv1:alice не в реестре")
	}
	if _, ok := hub.Node("srv1:bob"); !ok {
		t.Error("узел srv1:bob не в реестре")
	}
	if n := len(hub.Nodes()); n != 2 {
		t.Errorf("узлов = %d, хочу 2", n)
	}
}

// TestNodeIdentitySpawnByName — spawn на выбранный узел (host:os_user); имя
// сессии глобально: повторное имя на другом узле → NAME_IN_USE (409).
func TestNodeIdentitySpawnByName(t *testing.T) {
	ts, _, hub := testServer(t)
	capAlice := addFakeNodeWithUser(t, hub, "srv1", "alice", proto.ResSpawned, "%0")
	addFakeNodeWithUser(t, hub, "srv1", "bob", proto.ResSpawned, "%0")

	// Spawn на srv1:alice → 201.
	resp, err := httpPostJSON(t, ts.URL+"/api/v1/sessions/spawn", map[string]any{
		"name": "ni-sess", "dir": "/tmp/proj", "node": "srv1:alice"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("spawn на srv1:alice = %d (тело %s), хочу 201", resp.StatusCode, body)
	}
	var spawned struct {
		SID  string `json:"sid"`
		Node string `json:"node"`
	}
	if err := json.Unmarshal(body, &spawned); err != nil {
		t.Fatalf("распарсить ответ spawn: %v", err)
	}
	if spawned.Node != "srv1:alice" {
		t.Errorf("spawn.node = %q, хочу srv1:alice", spawned.Node)
	}
	if capAlice.SID == "" {
		t.Error("spawn не ушёл узлу srv1:alice")
	}

	// То же имя на srv1:bob → NAME_IN_USE (409): имя глобально.
	resp2, err := httpPostJSON(t, ts.URL+"/api/v1/sessions/spawn", map[string]any{
		"name": "ni-sess", "dir": "/tmp/proj", "node": "srv1:bob"})
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("повторное имя на srv1:bob = %d (тело %s), хочу 409", resp2.StatusCode, body2)
	}
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body2, &e); err != nil {
		t.Fatalf("распарсить 409: %v", err)
	}
	if e.Code != "NAME_IN_USE" {
		t.Errorf("код = %q, хочу NAME_IN_USE", e.Code)
	}
}

// TestNodeIdentityStateOsUser — /api/v1/state несёт OsUser узла: веб строит
// node_id = host:os_user из state и spawn'ит по этой идентичности. Без поля
// веб шлёт spawn на голый host → NO_NODE («узел не подключён»).
func TestNodeIdentityStateOsUser(t *testing.T) {
	ts, _, hub := testServer(t)
	addFakeNodeWithUser(t, hub, "srv1", "alice", proto.ResSpawned, "%0")

	resp, err := http.Get(ts.URL + "/api/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state = %d (тело %s), хочу 200", resp.StatusCode, body)
	}
	var sv struct {
		Nodes []struct {
			Host   string `json:"Host"`
			OsUser string `json:"OsUser"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &sv); err != nil {
		t.Fatalf("распарсить state: %v", err)
	}
	if len(sv.Nodes) != 1 {
		t.Fatalf("узлов в state = %d, хочу 1", len(sv.Nodes))
	}
	if sv.Nodes[0].Host != "srv1" || sv.Nodes[0].OsUser != "alice" {
		t.Errorf("state node = %s:%s, хочу srv1:alice",
			sv.Nodes[0].Host, sv.Nodes[0].OsUser)
	}
}
