package gateway

// host_ip сессии и проверка шлюза (remoteIP == sess.HostIP, раздел 6 ТЗ).
//
// Пути регистрации сессии и источник host_ip (таблица — docs/evidence):
//   - удалённый узел:   node.HostIP(hostPort(ws_url)) — локальный адрес
//     интерфейса к координатору (ws.go:279);
//   - встроенный узел:  coordinator.bind (embedded.go) — кодеры узла ходят в
//     шлюз через хост gateway_url (= coordinator.bind), источник = этот адрес;
//   - exec:             remoteIP(r) запроса /api/v1/jobs (jobs.go:139);
//   - runpilot run:     node.HostIP(coordHostPort(client.coordinator))
//     (run.go:172) — локальный адрес интерфейса к координатору.
//
// Для каждого пути генерирующий запрос сессии обязан пройти шлюз (не 403),
// если источник = host_ip сессии; с чужого адреса — 403.

import (
	"net/http"
	"testing"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/testutil/fakellm"
)

// TestHostIP_EmbeddedBind — встроенный узел: host_ip = coordinator.bind
// (127.0.0.1 в стенде). Генерирующий запрос с источника = host_ip проходит
// шлюз (не 403). Это и есть фикс 403 self-node (до фикса host_ip был
// захардкожен 127.0.0.1, а кодер ходил через coordinator.bind → 403).
func TestHostIP_EmbeddedBind(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_HOSTIP_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_HOSTIP_KEY")})

	// Сессия встроенного узла: host_ip = coordinator.bind = 127.0.0.1 (стенд).
	s := mkSession(t, st, model.SessionIdle)
	resp, body := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("встроенный узел (host_ip=bind): %d тело: %s, хочу 200 (не 403)", resp.StatusCode, body)
	}
}

// TestHostIP_ForeignIP403 — безопасность: сессия с host_ip = 127.0.0.1, но
// запрос с чужого адреса (10.9.9.9 ≠ host_ip) → 403. Фикс host_ip=bind не
// должен открывать доступ с чужих адресов.
func TestHostIP_ForeignIP403(t *testing.T) {
	a := fakellm.Start("srv-model")
	defer a.Close()
	t.Setenv("RUNPILOT_HOSTIP_KEY", "k")
	st, _, ts := testEnv(t, []config.Server{srvCfg("a", 10, 1, a.URL, "RUNPILOT_HOSTIP_KEY")})

	s := mkSession(t, st, model.SessionIdle)
	// Чужой адрес: host_ip сессии ≠ источник запроса (127.0.0.1 в стенде).
	_, _ = st.DB().Exec(`UPDATE session SET host_ip = '10.9.9.9' WHERE sid = ?`, s)
	resp, _ := postJSON(t, ts.URL+"/s/"+s+"/v1/chat/completions", chatBody("runpilot", ""))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой адрес: %d, хочу 403", resp.StatusCode)
	}
}
