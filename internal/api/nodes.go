package api

// Узлы и подключение (разделы 14, 5.3, 13.2, 14.2 ТЗ v2): подключение узла
// через /enroll/<token>/install.sh (CONTROL 1), загрузка бинарника по
// токену узла (CONTROL 4), self-update (CONTROL 2/3), удаление узла в обоих
// режимах (CONTROL 7), список каталогов, dirs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// bearerToken — Bearer-токен из заголовка (без префикса).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	return strings.TrimPrefix(h, "Bearer ")
}

// nodeTokenHostKey — ключ контекста: host, к которому привязан токен узла
// (N7: конфликт имени хоста).
type nodeTokenHostKey struct{}

// nodeTokenHost — host из токена узла (пусто, если не извлечён).
func nodeTokenHost(ctx context.Context) string {
	if v, ok := ctx.Value(nodeTokenHostKey{}).(string); ok {
		return v
	}
	return ""
}

// nodeAuth — аутентификация узла: локальный режим (пустой операторский токен)
// — без проверки; иначе операторский токен ИЛИ валидный токен узла
// (раздел 14.2 v2). Отозванный токен узла → 401 (CONTROL 4). Токен узла
// привязан к host — передаём его в контекст (N7: конфликт имени хоста).
func (s *Server) nodeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearerToken(r)
		if tok == s.token {
			next.ServeHTTP(w, r)
			return
		}
		if host, ok := s.store.VerifyNodeToken(tok); ok {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nodeTokenHostKey{}, host)))
			return
		}
		httpError(w, http.StatusUnauthorized, "UNAUTHORIZED", "неверный или отозванный токен узла")
	})
}

// --- подключение узла (раздел 14, CONTROL 1) ---

// handleNodeEnroll — POST /api/v1/nodes/enroll: создать enroll-токен.
func (s *Server) handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "METHOD", "POST")
		return
	}
	now := s.clk.Now()
	tok := store.NewNodeToken("", now)
	if err := s.store.CreateNodeToken(tok); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	base := s.requestBase(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"token":    tok.Token,
		"dist_url": base + "/enroll/" + tok.Token + "/runpilot",
		"install":  "curl -fsSL " + base + "/enroll/" + tok.Token + "/install.sh | bash",
	})
}

// requestBase — http(s)://host из запроса (для ссылок в install.sh).
func (s *Server) requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	// url.URL — чтобы не держать литерал "://" (R2: URL-литералы запрещены).
	return (&url.URL{Scheme: scheme, Host: r.Host}).String()
}

// handleEnroll — /enroll/<token>/<...>: install.sh | runpilot | claim. Токен в пути
// — аутентификация (чистая машина не знает операторского токена).
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if tok == "" {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет token")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/enroll/"+tok)
	rest = strings.Trim(rest, "/")
	switch rest {
	case "install.sh":
		s.enrollInstall(w, r, tok)
	case "claim":
		s.enrollClaim(w, r, tok)
	default:
		// /enroll/<token>/<file> — бинарник (runpilot).
		s.enrollFile(w, r, tok, rest)
	}
}

// tokenExists — токен существует (не обязательно привязан к host).
func (s *Server) tokenExists(token string) bool {
	if _, err := s.store.GetTokenByValue(token); err != nil {
		return false
	}
	return true
}

// enrollInstall — GET /enroll/<token>/install.sh.
func (s *Server) enrollInstall(w http.ResponseWriter, r *http.Request, token string) {
	if !s.tokenExists(token) {
		httpError(w, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен")
		return
	}
	base := s.requestBase(r)
	host, _ := os.Hostname()
	// COORD — базис запроса (https через tailscale serve) для загрузки
	// бинарника и claim. Координатор в конфиге — API-слушатель (bind:api_port):
	// канал узла GET /api/v1/node на веб-слушателе НЕ монтируется (только
	// API) — при https-адресе узел получил бы 401 «bad handshake».
	coord := s.requestBaseNoPath()
	// 14.1: бинарник с проверкой SHA-256, claim, секреты, «runpilot service
	// install node» + «runpilot tmux-setup», проверки tmux/qwen, linger → exit 3.
	// Скрипт НЕ держит терминал (никакого exec node): узел работает под
	// systemd, шелл остаётся свободным.
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
COORD="%s"
TOKEN="%s"
HOSTNAME_SUGGESTION="%s"
BIN="/usr/local/bin/runpilot"
if [ -w /usr/local/bin ]; then BIN="/usr/local/bin/runpilot"; else BIN="$HOME/.local/bin/runpilot"; fi
mkdir -p "$(dirname "$BIN")" "$HOME/.config/runpilot"
echo "runpilot: скачиваю бинарник…"
HDR="$(mktemp)"
trap 'rm -f "$HDR"' EXIT
curl -fsSL -D "$HDR" "$COORD/enroll/$TOKEN/runpilot" -o "$BIN"
chmod +x "$BIN"
# SHA-256 сверяется с заголовком X-RUNPILOT-Sha256 (14.1).
if command -v sha256sum >/dev/null 2>&1; then
  WANT_SHA="$(sed -n 's/^X-RUNPILOT-Sha256: //Ip' "$HDR" | tr -d '\r' | head -n 1)"
  if [ -n "$WANT_SHA" ]; then
    GOT_SHA="$(sha256sum "$BIN" | awk '{print $1}')"
    if [ "$WANT_SHA" != "$GOT_SHA" ]; then
      echo "runpilot: sha256 бинарника не сошёлся (ожидание $WANT_SHA, факт $GOT_SHA)"
      rm -f "$BIN"
      exit 1
    fi
  fi
fi
cfg="$HOME/.config/runpilot/config.yaml"
cat > "$cfg" <<EOF
client:
  coordinator: %s
  token_env: RUNPILOT_NODE_TOKEN
node:
  host: %s
EOF
echo "runpilot: привязываю узел…"
curl -fsSL -X POST "$COORD/enroll/$TOKEN/claim" \
  -H 'Content-Type: application/json' \
  -d "{\"host\":\"$(hostname)\"}"
echo "RUNPILOT_NODE_TOKEN=$TOKEN" > "$HOME/.config/runpilot/secrets.env"
chmod 600 "$HOME/.config/runpilot/secrets.env"
# Проверки окружения (14.1: tmux не ниже базового ТЗ, наличие qwen).
if ! command -v tmux >/dev/null 2>&1; then
  echo "runpilot: tmux не найден в PATH — установите tmux"
  exit 2
fi
TMUX_VER="$(tmux -V | awk '{print $2}')"
TMUX_MAJOR="${TMUX_VER%%.*}"
TMUX_MINOR="${TMUX_VER#*.}"; TMUX_MINOR="${TMUX_MINOR%%[!0-9]*}"
if [ "$TMUX_MAJOR" -lt 3 ] || { [ "$TMUX_MAJOR" -eq 3 ] && [ "$TMUX_MINOR" -lt 2 ]; }; then
  echo "runpilot: tmux $TMUX_VER слишком стар (нужна 3.2 или новее)"
  exit 2
fi
if ! command -v qwen >/dev/null 2>&1; then
  echo "runpilot: qwen (Qwen Code) не найден в PATH — установите Qwen Code"
  exit 2
fi
if ! timeout 15 qwen --version >/dev/null 2>&1; then
  # qwen не исполним: типичная причина — node (nvm) вне PATH непрямых
  # оболочек. Создаём symlink в ~/.local/bin (в PATH сервиса узла).
  QWEN_REAL="$(readlink -f "$(command -v qwen)" 2>/dev/null || command -v qwen)"
  if head -n 1 "$QWEN_REAL" 2>/dev/null | grep -q 'env node'; then
    NODE_BIN="$(ls -d "$HOME"/.nvm/versions/node/*/bin/node 2>/dev/null | sort -V | tail -n 1 || true)"
    if [ -n "$NODE_BIN" ]; then
      mkdir -p "$HOME/.local/bin"
      ln -sf "$NODE_BIN" "$HOME/.local/bin/node"
      echo "runpilot: создан $HOME/.local/bin/node → $NODE_BIN (node для qwen вне интерактивной шеллы)"
    fi
  fi
  if ! timeout 15 qwen --version >/dev/null 2>&1; then
    echo "runpilot: qwen не исполним (проверьте доступность node в PATH)"
    exit 2
  fi
fi
# Служба + tmux-интеграция (14.1): узел под systemd, скрипт завершается
# и не занимает терминал.
echo "runpilot: устанавливаю службу узла…"
"$BIN" service install node
"$BIN" tmux-setup
# linger: user-служба не переживёт выход пользователя.
LINGER="$(loginctl show-user "$USER" -p Linger --value 2>/dev/null || true)"
if [ "$LINGER" != "yes" ]; then
  echo ""
  echo "runpilot: linger не включён — после выхода пользователя узел остановится."
  echo "    Включите: sudo loginctl enable-linger $USER"
  exit 3
fi
if [ "$(id -u)" = "0" ]; then
  STATUS="$(systemctl is-active runpilot-node 2>/dev/null || true)"
else
  STATUS="$(systemctl --user is-active runpilot-node 2>/dev/null || true)"
fi
echo "runpilot: узел запущен (runpilot-node: ${STATUS:-unknown}) — «Узел $(hostname) подключён» появится в панели."
`, base, token, host, coord, host)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(script))
}

// enrollFile — GET /enroll/<token>/<file>: бинарник.
func (s *Server) enrollFile(w http.ResponseWriter, r *http.Request, token, file string) {
	if !s.tokenExists(token) {
		httpError(w, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен")
		return
	}
	p := s.distBinaryPath()
	if p == "" {
		httpError(w, http.StatusNotFound, "NO_DIST", "бинарник недоступен")
		return
	}
	s.serveBinary(w, r, p)
}

// enrollClaim — POST /enroll/<token>/claim {host}: привязать токен к узлу.
func (s *Server) enrollClaim(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "METHOD", "POST")
		return
	}
	var body struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Host == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "host обязателен")
		return
	}
	if err := s.store.BindTokenHost(token, body.Host, s.clk.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpError(w, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен")
			return
		}
		if errors.Is(err, store.ErrConstraint) {
			httpError(w, http.StatusConflict, "HOST_CONFLICT", "узел уже зарегистрирован")
			return
		}
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"host": body.Host, "ok": "true"})
}

// --- дистрибутив (раздел 14.2, CONTROL 4) ---

// distBinaryPath — путь бинарника: <dist_dir>/runpilot, иначе собственный.
func (s *Server) distBinaryPath() string {
	if dd, err := config.ExpandPath(s.cfg.Coordinator.DistDir); err == nil {
		if p := filepath.Join(dd, "runpilot"); fileExists(p) {
			return p
		}
	}
	if b, err := os.Executable(); err == nil {
		return b
	}
	return ""
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// serveBinary — отдать бинарник с заголовком X-RUNPILOT-Sha256.
func (s *Server) serveBinary(w http.ResponseWriter, r *http.Request, path string) {
	if h, err := sha256FileHex(path); err == nil {
		w.Header().Set("X-RUNPILOT-Sha256", h)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

func sha256FileHex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// handleDist — GET /api/v1/dist/<file>: только токен узла (CONTROL 4).
func (s *Server) handleDist(w http.ResponseWriter, r *http.Request) {
	tok := bearerToken(r)
	if _, ok := s.store.VerifyNodeToken(tok); !ok {
		httpError(w, http.StatusUnauthorized, "UNAUTHORIZED", "нужен токен узла")
		return
	}
	p := s.distBinaryPath()
	if p == "" {
		httpError(w, http.StatusNotFound, "NO_DIST", "бинарник недоступен")
		return
	}
	s.serveBinary(w, r, p)
}

// --- список/удаление/обновление узлов (CONTROL 7) ---

// handleNodes — GET /api/v1/nodes: подключённые узлы + их токены/панели.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	nodes := s.hub.Nodes()
	type nodeOut struct {
		NodeInfo
		Unmanaged int             `json:"unmanaged"`
		Health    *NodeHealthView `json:"health,omitempty"`
	}
	out := make([]nodeOut, 0, len(nodes))
	for _, n := range nodes {
		health, ok := s.hub.NodeHealth(n.Host)
		var hp *NodeHealthView
		if ok {
			hp = &health
		}
		out = append(out, nodeOut{NodeInfo: n, Unmanaged: s.hub.UnmanagedCount(n.Host), Health: hp})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleNodeDelete — DELETE /api/v1/nodes/<host>?mode=now|after_turns.
func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "now"
	}
	switch mode {
	case "now":
		// uninstall (best-effort) + отзыв токена + снять панели.
		if rep, err := s.dispatchToNodeTimeout(r.Context(), host, proto.New(proto.KindUninstall), s.nodeOpTimeout()); err == nil {
			s.log.Info("api: node delete: uninstall", "host", host, "result", rep.Result)
		}
		_ = s.store.RevokeNodeToken(host, s.clk.Now())
		s.hub.DetachHost(host)
		s.hub.Unregister(host)
		if s.sched != nil {
			s.sched.NodeLost(host)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"deleted": host})
	case "after_turns":
		s.MarkNodeRemoval(host)
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"removal": "after_turns", "host": host})
	default:
		httpError(w, http.StatusBadRequest, "BAD_MODE", "mode=now|after_turns")
	}
}

// handleNodeUpdate — POST /api/v1/nodes/<host>/update: принудительный
// self-update на версию координатора (CONTROL 2/3).
func (s *Server) handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if err := s.triggerNodeUpdate(r.Context(), host); err != nil {
		httpError(w, http.StatusServiceUnavailable, "UPDATE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"host": host, "result": "update-requested"})
}

// triggerNodeUpdate — отправить update-команду узлу (URL + sha256 + версия).
func (s *Server) triggerNodeUpdate(ctx context.Context, host string) error {
	p := s.distBinaryPath()
	if p == "" {
		return errors.New("бинарник недоступен")
	}
	sha, err := sha256FileHex(p)
	if err != nil {
		return err
	}
	m := proto.New(proto.KindUpdate)
	m.Version = s.version
	m.URL = s.requestBaseNoPath() + "/api/v1/dist/runpilot"
	m.SHA256 = sha
	if _, err := s.hub.DispatchToNode(ctx, host, m); err != nil {
		return err
	}
	return nil
}

// requestBaseNoPath — http://host:port (без пути) API координатора для URL
// дистрибутива (14.2). НЕ gateway_url: шлюз (gateway_port) раздаёт /s/<sid>/
// для кодеров, а /api/v1/dist/runpilot живёт только на API (api_port) — при
// gateway_url узел получал 404 и self-update молча падал (defect, W9).
func (s *Server) requestBaseNoPath() string {
	bind := s.cfg.Coordinator.Bind
	if !strings.Contains(bind, ":") {
		bind = fmt.Sprintf("%s:%d", bind, s.cfg.Coordinator.APIPort)
	}
	return httpScheme + bind
}

// handleNodeDirs — GET /api/v1/nodes/<host>/dirs?prefix=: каталоги из
// project_roots узла (автодополнение новой сессии, 13.2). roots — сами
// project_roots узла (подсказка с конкретными путями; у старых узлов
// поле пустое — веб показывает обобщённую подсказку).
func (s *Server) handleNodeDirs(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	m := proto.New(proto.KindListDirs)
	m.Prefix = r.URL.Query().Get("prefix")
	rep, err := s.dispatchToNodeTimeout(r.Context(), host, m, s.nodeOpTimeout())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "NODE", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"result": rep.Result, "dirs": splitLines(rep.Detail),
		"roots": rep.ProjectRoots,
	})
}

// handleNodeQwenSettings — POST /api/v1/nodes/<host>/qwen-settings (доп-3i):
// явная проверка/поправка ~/.qwen/settings.json узла. Веб показывает результат
// оператору (статус ok/changed/error + что изменено) ПЕРЕД запуском кодера.
func (s *Server) handleNodeQwenSettings(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if _, ok := s.hub.Node(host); !ok {
		httpError(w, http.StatusServiceUnavailable, "NODE", "узел не подключён")
		return
	}
	m := proto.New(proto.KindQwenSettings)
	m.ModelAlias = s.cfg.Profiles.Qwen.ModelAlias
	// item 3: размер контекста (min по серверам) → contextWindowSize кодера.
	m.ContextWindow = s.MinContextWindow()
	rep, err := s.dispatchToNodeTimeout(r.Context(), host, m, s.nodeOpTimeout())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, "NODE", err.Error())
		return
	}
	status, message := "ok", "настройки уже в порядке"
	switch rep.Result {
	case proto.ResQwenSettingsChanged:
		status, message = "changed", rep.Detail
	case proto.ResQwenSettingsError:
		status, message = "error", rep.Detail
	default:
		if rep.Detail != "" {
			message = rep.Detail
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status, "message": message})
}

// dispatchToNodeTimeout — команда узлу с коротким таймаутом.
func (s *Server) dispatchToNodeTimeout(ctx context.Context, host string, m proto.Msg, d time.Duration) (proto.Msg, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return s.hub.DispatchToNode(ctx, host, m)
}

// nodeOpTimeout — таймаут операции узла (dirs / uninstall; 13.2, 5.3).
func (s *Server) nodeOpTimeout() time.Duration {
	return time.Duration(s.cfg.Web.NodeOpTimeoutSec) * time.Second
}

// updateTimeout — таймаут self-update узла (14.2).
func (s *Server) updateTimeout() time.Duration {
	return time.Duration(s.cfg.Web.UpdateTimeoutSec) * time.Second
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// hasFeature — есть ли возможность в hello.features (proto 2, 15.5).
func hasFeature(features []string, want string) bool {
	for _, f := range features {
		if f == want {
			return true
		}
	}
	return false
}
