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
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"runpilot/internal/config"
	"runpilot/internal/model"
	"runpilot/internal/proto"
	"runpilot/internal/store"
)

// bearerToken — Bearer-токен из заголовка (без префикса).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	return strings.TrimPrefix(h, "Bearer ")
}

// nodeTokenIdentity — идентичность узла из токена (host, os_user).
type nodeTokenIdentity struct {
	host   string
	osUser string
}

// nodeTokenIdentityKey — ключ контекста: идентичность узла (host, os_user),
// к которой привязан токен (N7: конфликт имени хоста).
type nodeTokenIdentityKey struct{}

// tokenIdentity — идентичность узла из токена (пусто, если не извлечён).
func tokenIdentity(ctx context.Context) nodeTokenIdentity {
	if v, ok := ctx.Value(nodeTokenIdentityKey{}).(nodeTokenIdentity); ok {
		return v
	}
	return nodeTokenIdentity{}
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
		if host, osUser, ok := s.store.VerifyNodeToken(tok); ok {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(),
				nodeTokenIdentityKey{}, nodeTokenIdentity{host: host, osUser: osUser})))
			return
		}
		s.logAuthFail(r, http.StatusUnauthorized, "UNAUTHORIZED", "неверный или отозванный токен узла", "")
		httpError(w, http.StatusUnauthorized, "UNAUTHORIZED", "неверный или отозванный токен узла")
	})
}

// --- подключение узла (раздел 14, CONTROL 1) ---

// userNameRe — валидное имя пользователя Unix. Защита от инъекции: user и
// host встраиваются в install-команду (shell-строка).
var userNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

// hostRe — валидное имя хоста / tailscale-name. Защита от инъекции.
var hostRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// resolveTailnetHost — резолвит tailnet-hostname в IPv4 tailscale-IP через
// `tailscale status --json`. На координаторе MagicDNS может быть выключен и
// хост может отсутствовать в /etc/hosts — тогда ssh <user>@<host> не резолвит
// имя. Возвращает ("", false), если хост не peer tailnet или tailscale
// недоступен — вызывающий коды падает на исходный hostname.
func resolveTailnetHost(hostname string) (string, bool) {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return "", false
	}
	return parseTailnetHost(out, hostname)
}

// parseTailnetHost — извлекает IPv4 tailscale-IP `hostname` из JSON
// `tailscale status --json` (map Peer, HostName каждого peer). Возвращает
// ("", false), если хост не peer.
func parseTailnetHost(data []byte, hostname string) (string, bool) {
	var status struct {
		Peer map[string]struct {
			HostName     string   `json:"HostName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", false
	}
	for _, peer := range status.Peer {
		if peer.HostName != hostname {
			continue
		}
		for _, ip := range peer.TailscaleIPs {
			// IPv4 (tailscale 100.x.y.z) — без двоеточия.
			if !strings.Contains(ip, ":") {
				return ip, true
			}
		}
	}
	return "", false
}

// TailnetSelfIP — собственный IPv4 tailscale-IP этой машины (Self из
// `tailscale status --json`). Используется для from="<ip>" в authorized_keys
// узлов: узел принимает ключ координатора только с его tailnet-IP.
// Возвращает ("", false), если tailscale недоступен или IP не найден.
func TailnetSelfIP() (string, bool) {
	out, err := exec.Command("tailscale", "status", "--json").Output()
	if err != nil {
		return "", false
	}
	return parseTailnetSelfIP(out)
}

// parseTailnetSelfIP — IPv4 из Self.TailscaleIPs JSON `tailscale status --json`.
func parseTailnetSelfIP(data []byte) (string, bool) {
	var status struct {
		Self struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", false
	}
	for _, ip := range status.Self.TailscaleIPs {
		// IPv4 (tailscale 100.x.y.z) — без двоеточия.
		if !strings.Contains(ip, ":") {
			return ip, true
		}
	}
	return "", false
}

// enrollSSHCommand — ssh-команда автоустановки узла (нереинтерактивно).
// keyFlags — « -i <путь>» на каждый ключ coordinator.ssh_keys. password —
// парольная авторизация: BatchMode снят (он гасит промпт пароля), ключ
// пробуют первым, пароль — fallback через SSH_ASKPASS (задаётся в
// runEnrollInstall, не в самой команде).
func enrollSSHCommand(target, curlCmd, keyFlags string, password bool) string {
	batch := "-o BatchMode=yes "
	if password {
		batch = ""
	}
	return "ssh " + batch + "-o ConnectTimeout=10 " +
		"-o StrictHostKeyChecking=accept-new" + keyFlags +
		" " + target + " '" + curlCmd + "'"
}

// sshKeyFlags — « -i <путь>» на каждый ключ coordinator.ssh_keys (с
// расширением ~). Пусто, если список пуст (ssh сам перебирает ~/.ssh/).
func (s *Server) sshKeyFlags() string {
	var flags string
	for _, k := range s.cfg.Coordinator.SSHKeys {
		p, err := config.ExpandPath(k)
		if err != nil {
			continue
		}
		flags += " -i " + p
	}
	return flags
}

// handleNodeEnroll — POST /api/v1/nodes/enroll: создать enroll-токен.
// host (опц.) — целевая машина (tailscale-name): install выполняется через
// ssh <user>@<host> (оператор со своей машины, без sudo на целевой).
// user (опц.) — от какого пользователя OS работать узел. Пусто = локальный
// curl (старое поведение).
func (s *Server) handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "METHOD", "POST")
		return
	}
	var body struct {
		User        string `json:"user"`
		Host        string `json:"host"`
		Password    string `json:"password"`
		AutoInstall bool   `json:"auto_install"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	user := strings.TrimSpace(body.User)
	host := strings.TrimSpace(body.Host)
	password := strings.TrimSpace(body.Password)
	if user != "" && !userNameRe.MatchString(user) {
		httpError(w, http.StatusBadRequest, "BAD_USER",
			"некорректное имя пользователя (допустимы [a-z_][a-z0-9_-]*)")
		return
	}
	if host != "" && !hostRe.MatchString(host) {
		httpError(w, http.StatusBadRequest, "BAD_HOST",
			"некорректное имя хоста (допустимы [a-z0-9.-])")
		return
	}
	now := s.clk.Now()
	tok := store.NewNodeToken("", "", now)
	if err := s.store.CreateNodeToken(tok); err != nil {
		httpError(w, http.StatusInternalServerError, "STORE", err.Error())
		return
	}
	base := s.requestBase(r)
	curlCmd := "curl -fsSL " + base + "/enroll/" + tok.Token + "/install.sh | bash"
	install := curlCmd
	if host != "" {
		// ssh на целевую машину: узел работает от <user> (или от пользователя
		// ssh, если user пусто).
		// MagicDNS может быть выключен / хост не в /etc/hosts: резолвим
		// tailnet-имя в IP, иначе ssh не найдёт хост.
		sshHost := host
		if s.resolveHost != nil {
			if ip, ok := s.resolveHost(host); ok {
				sshHost = ip
			}
		}
		target := sshHost
		if user != "" {
			target = user + "@" + sshHost
		}
		if body.AutoInstall {
			// Автоустановка: ssh выполняет координатор (нереинтерактивно).
			install = enrollSSHCommand(target, curlCmd, s.sshKeyFlags(), password != "")
		} else {
			// Ручной режим: оператор запускает команду со своей машины.
			install = "ssh " + target + " '" + curlCmd + "'"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if body.AutoInstall && host != "" {
		// Ауто-установка: ssh в фоне, результат — по SSE (NODE_ENROLL).
		go s.runEnrollInstall(host, install, password)
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"token":  tok.Token,
			"status": "installing",
			"host":   host,
		})
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"token":    tok.Token,
		"dist_url": base + "/enroll/" + tok.Token + "/runpilot",
		"install":  install,
	})
}

// runEnrollInstall — фоновая ssh-установка узла (auto_install). Результат
// (ok/failed + вывод) уходит в веб событием SSE NODE_ENROLL (Server = host).
// password (опц.) — парольная авторизация ssh через SSH_ASKPASS (force):
// askpass-скрипт выводит $SSHPASS. Пароль только в env подпроцесса, не в
// командной строке (невидим в ps).
func (s *Server) runEnrollInstall(host, install, password string) {
	ctx, cancel := context.WithTimeout(context.Background(), enrollInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", install)
	if password != "" {
		if f, err := os.CreateTemp("", "runpilot-askpass-*"); err == nil {
			_, _ = f.WriteString("#!/bin/bash\nprintf '%s' \"$SSHPASS\"\n")
			_ = f.Close()
			_ = os.Chmod(f.Name(), askPassPerm)
			defer os.Remove(f.Name())
			cmd.Env = append(os.Environ(),
				"SSHPASS="+password,
				"SSH_ASKPASS="+f.Name(),
				"SSH_ASKPASS_REQUIRE=force",
			)
		}
	}
	out, err := cmd.CombinedOutput()
	result := "ok"
	if err != nil {
		result = "failed"
	}
	s.log.Info("api: node enroll install", "host", host, "result", result, "err", err)
	payload, _ := json.Marshal(map[string]any{
		"result": result, "output": strings.TrimSpace(string(out)),
	})
	_ = s.store.EventRecord(model.Event{
		TS: s.clk.Now(), Kind: model.KindNodeEnroll, Server: host, Payload: payload,
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

// nodeEnrollFileSetup — шелл создания config.yaml и secrets.env на узле с
// режимом 0600 НЕЗАВИСИМО от umask: каждый файл — `chmod 600` после создания.
// Без этого `cat >`/`echo >` наследуют umask (на umask 0022 дают 0664 → doctor
// file_mode FAIL: «config.yaml=664»). Общий для install.sh (enrollInstall) и
// теста TestNodeEnrollFileSetupUmask. cfgDir — каталог (в install.sh:
// `$HOME/.config/runpilot`; в тесте — tmpdir).
// node.host — ПУСТОЙ: узел определяет hostname сам (os.Hostname()), а не из
// конфига (иначе install.sh, сгенерированный на координаторе, вписал бы
// hostname координатора → «конфликт имени хоста» при claim по hostname узла).
// profiles.qwen.command — абсолютный путь к кодеру (command -v в login-шелле):
// узел запускает кодера с PATH, в начало которого добавлен каталог кодера
// (там же node из nvm). Симлинков в ~/.local/bin НЕТ (правка окружения
// оператора запрещена).
func nodeEnrollFileSetup(cfgDir, coordinator, token string) string {
	cfg := cfgDir + "/config.yaml"
	secrets := cfgDir + "/secrets.env"
	return fmt.Sprintf(`cfg="%s"
secrets="%s"
# config.yaml: создаём только если нет — не затираем operator-конфиг
# (project_roots_default и пр. должны пережить повторный auto_install).
if [ ! -f "$cfg" ]; then
  cat > "$cfg" <<'YAML'
client:
  coordinator: %s
  token_env: RUNPILOT_NODE_TOKEN
node:
  host: ""
YAML
  # Телеметрия GPU (W13 этап 5): --gpu / --gpu-server.
  if [ -n "${GPU_SERVER:-}" ]; then
    {
      printf '  gpu: %%s\n' "${GPU:-nvidia}"
      printf '  gpu_server: %%s\n' "$GPU_SERVER"
    } >> "$cfg"
  fi
  QWEN_PATH="$(command -v qwen || true)"
  if [ -n "$QWEN_PATH" ]; then
    printf 'profiles:\n  qwen:\n    command: %%s\n' "$QWEN_PATH" >> "$cfg"
  fi
  chmod 600 "$cfg"
fi
# secrets.env — всегда (токен может обновиться при ротации).
echo "RUNPILOT_NODE_TOKEN=%s" > "$secrets"
chmod 600 "$secrets"
`, cfg, secrets, coordinator, token)
}

// enrollInstall — GET /enroll/<token>/install.sh.
func (s *Server) enrollInstall(w http.ResponseWriter, r *http.Request, token string) {
	if !s.tokenExists(token) {
		s.logAuthFail(r, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен", "enroll")
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
# Телеметрия GPU (W13 этап 5): --gpu <nvidia|amd>, --gpu-server <имя сервера>.
GPU=""
GPU_SERVER=""
while [ $# -gt 0 ]; do
  case "$1" in
    --gpu)
      [ $# -ge 2 ] || { echo "runpilot: --gpu требует значение"; exit 2; }
      GPU="$2"; shift 2 ;;
    --gpu-server)
      [ $# -ge 2 ] || { echo "runpilot: --gpu-server требует значение"; exit 2; }
      GPU_SERVER="$2"; shift 2 ;;
    *) shift ;;
  esac
done
# Останавливаем запущенный узел (если есть), чтобы заменить бинарник:
# иначе curl -o $BIN → exit 23 (text-file-busy, ELF занят). Сервис перезапустится
# ниже (service install node), если установка дойдёт до конца.
if [ "$(id -u)" = "0" ]; then
  systemctl stop runpilot-node 2>/dev/null || true
else
  systemctl --user stop runpilot-node 2>/dev/null || true
fi
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
%s
echo "runpilot: привязываю узел…"
curl -fsSL -X POST "$COORD/enroll/$TOKEN/claim" \
  -H 'Content-Type: application/json' \
  -d "{\"host\":\"$(hostname)\",\"os_user\":\"$(id -un)\"}"
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
  echo "runpilot: qwen не исполним (проверьте доступность node в PATH)"
  exit 2
fi
# Путь к кодеру уже записан в config.yaml (profiles.qwen.command, см.
# nodeEnrollFileSetup). Узел запускает кодера с PATH, в начало которого
# добавлен каталог кодера (там же node из nvm). Симлинков НЕ создаём
# (правка окружения оператора запрещена).
# Служба + tmux-интеграция (14.1): узел под systemd, скрипт завершается
# и не занимает терминал.
echo "runpilot: устанавливаю службу узла…"
"$BIN" service install node
"$BIN" tmux-setup
# linger: user-служба не переживёт выход пользователя. $(id -un) — фактический
# пользователь (при sudo -u <user> $USER может остаться операторским).
NODE_USER="$(id -un)"
LINGER="$(loginctl show-user "$NODE_USER" -p Linger --value 2>/dev/null || true)"
if [ "$LINGER" != "yes" ]; then
  echo ""
  echo "runpilot: linger не включён — после выхода пользователя узел остановится."
  echo "    Включите: sudo loginctl enable-linger $NODE_USER"
  exit 3
fi
if [ "$(id -u)" = "0" ]; then
  STATUS="$(systemctl is-active runpilot-node 2>/dev/null || true)"
else
  STATUS="$(systemctl --user is-active runpilot-node 2>/dev/null || true)"
fi
echo "runpilot: узел запущен (runpilot-node: ${STATUS:-unknown}) — «Узел $(hostname) подключён» появится в панели."
`, base, token, host,
		nodeEnrollFileSetup("$HOME/.config/runpilot", coord, token))
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(script))
}

// enrollFile — GET /enroll/<token>/<file>: бинарник.
func (s *Server) enrollFile(w http.ResponseWriter, r *http.Request, token, file string) {
	if !s.tokenExists(token) {
		s.logAuthFail(r, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен", "enroll")
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

// enrollClaim — POST /enroll/<token>/claim {host, os_user}: привязать токен
// к узлу (host, os_user).
func (s *Server) enrollClaim(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "METHOD", "POST")
		return
	}
	var body struct {
		Host   string `json:"host"`
		OsUser string `json:"os_user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Host == "" {
		httpError(w, http.StatusBadRequest, "BAD_REQUEST", "host обязателен")
		return
	}
	if err := s.store.BindTokenHost(token, body.Host, body.OsUser, s.clk.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.logAuthFail(r, http.StatusUnauthorized, "BAD_TOKEN", "enroll-токен недействителен", "enroll")
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

// distVersion — версия бинарника в dist_dir (`<dist> version`); для защиты от
// stale dist (W12, ТЗ 14). Ошибка — бинарник недоступен/не runpilot.
// `version` выводит две строки (версия + коммит, git-сборка) — берём первую.
func (s *Server) distVersion() (string, error) {
	p := s.distBinaryPath()
	if p == "" {
		return "", errors.New("бинарник недоступен")
	}
	ctx, cancel := context.WithTimeout(context.Background(), distVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "version").Output()
	if err != nil {
		return "", err
	}
	return firstLine(string(out)), nil
}

// firstLine — первая строка вывода (команда `version` отдаёт «версия\nкоммит»;
// сверка идёт по версии, коммит игнорируем).
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
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
	if _, _, ok := s.store.VerifyNodeToken(tok); !ok {
		s.logAuthFail(r, http.StatusUnauthorized, "UNAUTHORIZED", "нужен токен узла", "")
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

// handleNodeVersion — GET /api/v1/node/version: версия координатора (токен
// узла, W12). Doctor узла сверяет свою версию с версией координатора и выносит
// WARN при несовпадении (автообновления нет — ТЗ 14).
func (s *Server) handleNodeVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": s.version})
}

// --- список/удаление/обновление узлов (CONTROL 7) ---

// handleNodes — GET /api/v1/nodes: подключённые узлы + их токены/панели.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	nodes := s.hub.Nodes()
	type nodeOut struct {
		NodeInfo
		Unmanaged       int             `json:"unmanaged"`
		Health          *NodeHealthView `json:"health,omitempty"`
		VersionMismatch bool            `json:"version_mismatch"`
	}
	out := make([]nodeOut, 0, len(nodes))
	for _, n := range nodes {
		health, ok := s.hub.NodeHealth(n.Host)
		var hp *NodeHealthView
		if ok {
			hp = &health
		}
		// W12 (ТЗ 14): несовпадение версии узла и координатора — бейдж в вебе.
		mismatch := s.version != "" && n.RUNPILOTVersion != s.version
		out = append(out, nodeOut{
			NodeInfo: n, Unmanaged: s.hub.UnmanagedCount(n.Host), Health: hp,
			VersionMismatch: mismatch,
		})
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
		// Hub/dispatch/Unregister — per-nodeID (host:os_user), а DELETE-URL
		// несёт host → резолвим nodeID. Узла нет в hub = offline → отмена
		// удаления (503): снять сервис по ssh невозможно.
		nodeID, ok := s.nodeIDForHost(host)
		if !ok {
			s.log.Warn("api: node delete: node offline", "host", host)
			httpError(w, http.StatusServiceUnavailable, "NODE_OFFLINE",
				"узел не подключён, не удалось снять сервис: "+host)
			return
		}
		// uninstall: узел сам снимает сервис (systemctl disable+stop, rm
		// бинарника/конфига). При ошибке — отмена удаления (узел остаётся в
		// координаторе) + реальная ошибка пользователю.
		rep, err := s.dispatchToNodeTimeout(r.Context(), nodeID, proto.New(proto.KindUninstall), s.nodeOpTimeout())
		if err != nil {
			s.log.Warn("api: node delete: uninstall failed", "host", host, "err", err)
			httpError(w, http.StatusServiceUnavailable, "NODE_OFFLINE",
				"узел недоступен, не удалось снять: "+err.Error())
			return
		}
		if rep.Result != proto.ResUninstalled {
			detail := rep.Detail
			if detail == "" {
				detail = "узел не подтвердил снятие"
			}
			s.log.Warn("api: node delete: uninstall failed", "host", host, "detail", detail)
			httpError(w, http.StatusServiceUnavailable, "UNINSTALL_FAILED",
				"не удалось удалить узел: "+detail)
			return
		}
		s.log.Info("api: node delete: uninstall", "host", host, "result", rep.Result)
		// Токен узла per-node (host:os_user): извлекаем host и os_user из nodeID.
		_ = s.store.RevokeNodeToken(hostOf(nodeID), osUserOf(nodeID), s.clk.Now())
		s.hub.DetachHost(nodeID)
		s.hub.Unregister(nodeID)
		if s.sched != nil {
			s.sched.NodeLost(nodeID)
		}
		// Строки external_process узла → GONE (убираются из live-списка).
		_ = s.store.ExternalDeleteByNode(nodeID, s.clk.Now())
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

// UpdateOp — асинхронная операция self-update узла (W12 7b).
type UpdateOp struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	Stage     string    `json:"stage"`
	Result    string    `json:"result,omitempty"`
	Version   string    `json:"version,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// handleNodeUpdate — POST /api/v1/nodes/<host>/update: асинхронный self-update
// на версию координатора (CONTROL 2/3, W12 7b). Возвращает 202 + id операции.
// W12 (ТЗ 14): если бинарник в dist_dir не совпадает по версии с координатором
// (stale dist) — отказ 409 DIST_MISMATCH, обновление не запускается.
func (s *Server) handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	// Защита от stale dist: версия бинарника в dist_dir = версии координатора.
	// Если dist не runpilot-бинарник (version не отвечает) — пропускаем (узел
	// проверит selftest + версию); если отвечает и не совпадает — отказ.
	if dv, err := s.distVersion(); err == nil && s.version != "" && dv != s.version {
		httpError(w, http.StatusConflict, "DIST_MISMATCH",
			fmt.Sprintf("версия dist %s != версии координатора %s — обновите dist (deploy-coordinator.sh)", dv, s.version))
		return
	}
	op := &UpdateOp{
		ID:        fmt.Sprintf("upd-%d", s.clk.Now().UnixNano()),
		Host:      host,
		Stage:     "started",
		StartedAt: s.clk.Now(),
	}
	s.updateMu.Lock()
	if s.updateOps == nil {
		s.updateOps = map[string]*UpdateOp{}
	}
	s.updateOps[op.ID] = op
	s.updateMu.Unlock()

	go s.runNodeUpdate(op)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"id": op.ID, "host": host})
}

// runNodeUpdate — фоновое выполнение self-update (W12 7b).
func (s *Server) runNodeUpdate(op *UpdateOp) {
	defer func() {
		op.EndedAt = s.clk.Now()
		s.updateMu.Lock()
		opCopy := *op
		s.updateOps[op.ID] = &opCopy
		s.updateMu.Unlock()
	}()
	if err := s.triggerNodeUpdate(context.Background(), op.Host); err != nil {
		op.Stage = "failed"
		op.Result = err.Error()
		return
	}
	op.Stage = "done"
	op.Result = "ok"
	op.Version = s.version
}

// handleNodeUpdateStatus — GET /api/v1/nodes/<host>/update/<id>: статус
// асинхронного self-update (W12 7b).
func (s *Server) handleNodeUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.updateMu.Lock()
	op, ok := s.updateOps[id]
	s.updateMu.Unlock()
	if !ok {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет операции")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(op)
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
	m.BuildTime = s.buildTimeUnix // W12: детект отката узлом
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
	dirs := splitLines(rep.Detail)
	// W13 этап 8: фильтр по project_dir_match (подстрока в пути).
	if match := s.cfg.Node.ProjectDirMatch; match != "" {
		filtered := make([]string, 0, len(dirs))
		for _, d := range dirs {
			if strings.Contains(d, match) {
				filtered = append(filtered, d)
			}
		}
		dirs = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"result": rep.Result, "dirs": dirs,
		"roots": rep.ProjectRoots,
		// Домашний каталог пользователя узла — каталог по умолчанию в форме
		// «Новая сессия» (ТЗ «запуск на узле выбранного пользователя»).
		"home": rep.Home,
	})
}

// hostOf — имя хоста из идентичности узла (host:os_user): часть до ':'.
// Токены узла per-node (host:os_user), а hub/планировщик/dispatch — per-nodeID.
// Пустой os_user (старые узлы) → nodeID == host.
func hostOf(nodeID string) string {
	if i := strings.Index(nodeID, ":"); i >= 0 {
		return nodeID[:i]
	}
	return nodeID
}

// osUserOf — пользователь ОС из идентичности узла (host:os_user): часть после
// первого ':'. Пустой os_user (старые узлы) → nodeID == host → "".
func osUserOf(nodeID string) string {
	if i := strings.Index(nodeID, ":"); i >= 0 {
		return nodeID[i+1:]
	}
	return ""
}

// nodeIDForHost — nodeID (host:os_user) первого узла с данным host. Hub/
// dispatch/Unregister — per-nodeID, а DELETE-URL несёт host. Пустой os_user
// (старые узлы) → nodeID == host.
func (s *Server) nodeIDForHost(host string) (string, bool) {
	for _, n := range s.hub.Nodes() {
		if n.Host == host {
			return n.ID(), true
		}
	}
	return "", false
}

// nodeSettingsCheck — результат проверки ~/.qwen/settings.json одного узла.
type nodeSettingsCheck struct {
	Node   string `json:"node"`
	Level  string `json:"level"`  // PASS | FAIL | WARN
	Detail string `json:"detail"`
}

// handleNodeSettingsCheck — GET /api/v1/nodes/settings-check (doctor, ТЗ
// «запуск сессий на узле выбранного пользователя»): диспатч check_settings
// каждому подключённому узлу; узел читает свой ~/.qwen/settings.json и
// возвращает PASS/FAIL/WARN. Ответ — поузловая таблица.
func (s *Server) handleNodeSettingsCheck(w http.ResponseWriter, r *http.Request) {
	nodes := s.hub.Nodes()
	out := make([]nodeSettingsCheck, 0, len(nodes))
	for _, n := range nodes {
		nodeID := n.ID()
		m := proto.New(proto.KindCheckSettings)
		rep, err := s.dispatchToNodeTimeout(r.Context(), nodeID, m, s.nodeOpTimeout())
		if err != nil {
			out = append(out, nodeSettingsCheck{Node: nodeID, Level: "WARN", Detail: "узел недоступен: " + err.Error()})
			continue
		}
		out = append(out, nodeSettingsCheck{Node: nodeID, Level: rep.Result, Detail: rep.Detail})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"nodes": out})
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

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

