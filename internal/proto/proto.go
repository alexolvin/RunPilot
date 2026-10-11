// Package proto — JSON-сообщения канала узла (разделы 13/15.5 ТЗ).
//
// Каждое сообщение несёт поля type и proto. Текущая мажорная версия — 2;
// координатор принимает N и N−1 (совместимость), hello несёт features.
package proto

import (
	"fmt"
	"time"
)

// Коды result в reply (разделы 7/8 ТЗ).
const (
	ResSent        = "SENT"
	ResAlreadyBusy = "ALREADY_BUSY"
	ResPaneGone    = "PANE_GONE"
	ResPaneWaitUI  = "PANE_WAIT_UI"
	ResPaneChanged = "PANE_CHANGED"
	ResEmptyInput  = "EMPTY_INPUT"
	ResBadKeys     = "BAD_KEYS"
	ResDrain       = "DRAIN"

	// Ввод в панель (раздел 13.1 ТЗ, v2).
	ResPasted          = "PASTED"
	ResPasteMismatch   = "PASTE_MISMATCH"
	ResPasteSubmitted  = "PASTE_SUBMITTED"
	ResApproved        = "APPROVED"
	ResApproveNoEffect = "APPROVE_NO_EFFECT"

	// proto 2: сессии, обновление, каталоги (разделы 13.2/13.5/14.2 ТЗ, v2).
	ResSpawned      = "SPAWNED"
	ResRespawned    = "RESPAWNED"
	ResDirForbidden = "DIR_FORBIDDEN"
	ResDirNotFound  = "DIR_NOT_FOUND"
	ResPrecheckFailed = "PRECHECK_FAILED" // W13 этап 7: предпроверка (409)
	ResKilled         = "KILLED"
	ResProcessChanged = "PROCESS_CHANGED"
	ResProcessGone    = "PROCESS_GONE"
	ResUpdated        = "UPDATED"
	ResUpdateFailed = "NODE_UPDATE_FAILED"
	// W12 (ТЗ 14): защита self-update. Noop — целевая версия = текущей (узел
	// ничего не делает и не перезапускается). Refused — откат без downgrade=true
	// или версия скачанного бинарника ≠ ожидаемой.
	ResUpdateNoop    = "UPDATE_NOOP"
	ResUpdateRefused = "UPDATE_REFUSED"
	ResUninstalled   = "UNINSTALLED"
	ResAgentExit    = "AGENT_EXIT"

	// adopt (8.3 X1): «Перезапустить под runpilot» — панель принята в управление.
	ResAdopted  = "ADOPTED"
	ResNotIdle  = "NOT_IDLE"
	ResManaged  = "MANAGED"

	// Терминал (13.4 ТЗ): PTY открыт (tmux attach запущен).
	ResPtyOpened = "PTY_OPENED"
)

// Kind — значения поля type.
type Kind string

const (
	// Узел → координатор.
	KindHello        Kind = "hello"
	KindPanes        Kind = "panes"
	KindPane         Kind = "pane"
	KindPaneGone     Kind = "pane_gone"
	KindGPU          Kind = "gpu"
	KindReply        Kind = "reply"
	KindAgentExit    Kind = "agent_exit"
	KindNodeHealth   Kind = "node_health"
	KindDirs         Kind = "dirs"
	KindUpdateStatus Kind = "update_status"
	KindExternal     Kind = "external_process"
	KindUnmanaged    Kind = "unmanaged_panes"
	// EXEC-корень мёртв без finish (kill -9/падение): узел хоста сообщил, что
	// PID корня исчез или время старта не совпало (SID + PID). Координатор
	// снимает аренду + ведёт сессию в GONE (T17: слот свободен ≤ 3 с).
	KindExecRootDead Kind = "exec_root_dead"

	// Координатор → узел.
	KindDispatch    Kind = "dispatch"
	KindSendKeys    Kind = "send_keys"
	KindCapture     Kind = "capture"
	KindScreen      Kind = "screen"
	KindPaste       Kind = "paste"
	KindApprove     Kind = "approve"
	KindCompress    Kind = "compress"
	KindSetStatus   Kind = "set_status"
	KindSetSummary  Kind = "set_summary"
	KindDrain       Kind = "drain"
	KindSpawn       Kind = "spawn"
	KindRespawn     Kind = "respawn"
	KindKillSession Kind = "kill_session"
	KindReleasePane Kind = "release_pane"
	KindKillProcess Kind = "kill_process"
	KindListDirs    Kind = "list_dirs"
	KindUpdate      Kind = "update"
	KindUninstall   Kind = "uninstall"
	KindConfig      Kind = "config"
	KindExecRoots   Kind = "exec_roots" // EXEC-корни (PID+время старта+sid) узлу
	KindAdopt       Kind = "adopt"

	// Терминал в браузере (13.4 ТЗ, v2). pty_open/pty_close — команды
	// координатора; pty_exit — узел сообщает о завершении tmux attach.
	KindPtyOpen  Kind = "pty_open"
	KindPtyClose Kind = "pty_close"
	KindPtyExit  Kind = "pty_exit"
)

// Все значения type протокола (для валидации входящих сообщений).
var AllKinds = map[Kind]bool{
	KindHello: true, KindPanes: true, KindPane: true, KindPaneGone: true, KindGPU: true,
	KindReply: true, KindAgentExit: true, KindNodeHealth: true, KindDirs: true,
	KindUpdateStatus: true, KindExternal: true, KindUnmanaged: true,
	KindExecRootDead: true,
	KindDispatch: true, KindSendKeys: true, KindCapture: true, KindScreen: true,
	KindPaste: true, KindApprove: true, KindCompress: true,
	KindSetStatus: true, KindSetSummary: true, KindDrain: true,
	KindSpawn: true, KindRespawn: true, KindKillSession: true, KindReleasePane: true,
	KindKillProcess: true, KindListDirs: true, KindUpdate: true, KindUninstall: true,
	KindConfig: true, KindExecRoots: true, KindAdopt: true,
	KindPtyOpen: true, KindPtyClose: true, KindPtyExit: true,
}

// Msg — одно сообщение канала узла. Поля, не относящиеся к type,
// отсутствуют в JSON (omitempty).
type Msg struct {
	Type  Kind `json:"type"`
	Proto int  `json:"proto"`

	// hello
	Host        string   `json:"host,omitempty"`
	HostIP      string   `json:"host_ip,omitempty"`
	RUNPILOTVersion  string   `json:"runpilot_version,omitempty"`
	TmuxVersion string   `json:"tmux_version,omitempty"`
	OS          string   `json:"os,omitempty"`
	Sockets     []string `json:"sockets,omitempty"`
	Features    []string `json:"features,omitempty"` // proto 2: spawn/paste/pty/update/external/agent_run

	// panes
	Panes []Pane `json:"panes,omitempty"`

	// external_process (8.2): внешние кодеры вне tmux/runpilot.
	Externals []ExternalProc `json:"externals,omitempty"`

	// exec_roots: EXEC-корни сессий (PID+время старта+sid) координатор → узел
	// (правило «кодер вне очереди»): кодер управляем, если потомок PID и время
	// старта корневого процесса совпадает с сохранённым.
	ExecRoots []ExecRoot `json:"exec_roots,omitempty"`

	// unmanaged_panes (8.3 X1): панели с кодером без @runpilot_sid (полный список
	// каждого цикла; пустой список тоже шлётся — координатор сверяет живое).
	Unmanaged []UnmanagedPane `json:"unmanaged,omitempty"`

	// pane
	Pane *Pane `json:"pane,omitempty"`

	// gpu (Э6)
	Server string    `json:"server,omitempty"`
	Cards  []GPUCard `json:"cards,omitempty"`

	// reply
	CmdID  string `json:"cmd_id,omitempty"`
	Result string `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`

	// screen (ANSI-снимок, раздел 15.4 ТЗ): размеры панели
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	// dispatch
	SID           string `json:"sid,omitempty"`
	PaneID        string `json:"pane_id,omitempty"`
	Mode          string `json:"mode,omitempty"`
	ExpectHash    uint64 `json:"expect_hash,omitempty"`
	ResumeText    string `json:"resume_text,omitempty"`
	LaunchCommand string `json:"launch_command,omitempty"`

	// send_keys
	Keys []string `json:"keys,omitempty"`

	// capture
	Lines int `json:"lines,omitempty"`

	// set_status / set_summary / paste (текст)
	Text string `json:"text,omitempty"`

	// paste: отправить submit-клавишу после вставки (compress)
	SubmitAfter bool `json:"submit_after,omitempty"`

	// drain
	On bool `json:"on,omitempty"`

	// spawn / respawn (13.2/13.5): name/dir/env/args; respawn добавляет pane_id.
	Name string            `json:"name,omitempty"`
	Dir  string            `json:"dir,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Args []string          `json:"args,omitempty"`

	// list_dirs / dirs (13.2): префикс автодополнения и список каталогов.
	Prefix string   `json:"prefix,omitempty"`
	Dirs   []string `json:"dirs,omitempty"`

	// kill_process (6.7/8.2): цель сигнала. KProcUID/KProcExe — сверка
	// идентичности процесса узлом (UID+cmdline+start_time, иначе PROCESS_CHANGED).
	PID        int    `json:"pid,omitempty"`
	StartTime  int64  `json:"start_time,omitempty"`
	Signal     string `json:"signal,omitempty"`
	KProcUID   int    `json:"kproc_uid,omitempty"`
	KProcExe   string `json:"kproc_exe,omitempty"`

	// capture (proto 2): просить ANSI-снимок.
	Ansi bool `json:"ansi,omitempty"`

	// update / uninstall / config (14.2/5.3/14.4).
	Version      string   `json:"version,omitempty"`
	URL          string   `json:"url,omitempty"`
	SHA256       string   `json:"sha256,omitempty"`
	// BuildTime — commit timestamp (vcs.time, unix-сек) бинарника, который
	// ставит координатор. Узел сверяет со своим: target < current → откат,
	// допускается только при Downgrade=true. 0 — неизвестен (e2e без git).
	BuildTime int64 `json:"build_time,omitempty"`
	// Downgrade — явное разрешение отката на более старую версию (W12, ТЗ 14).
	Downgrade bool `json:"downgrade,omitempty"`
	ProjectRoots []string `json:"project_roots,omitempty"`
	// config (W9 доп-3c): эндпоинт qwen code — узел приводит
	// ~/.qwen/settings.json в порядок (selectedType=openai, нет
	// modelProviders с id=model_alias), чтобы окружение сессии
	// OPENAI_BASE_URL шлюза не перебивалось собственным конфигом qwen.
	ModelAlias string `json:"model_alias,omitempty"`
	GatewayURL string `json:"gateway_url,omitempty"`
	// item 3: размер контекста модели (min по серверам из /v1/models) — узел
	// пишет его в settings.model.generationConfig.contextWindowSize, чтобы
	// qwen объявлял реальный контекст, а не гадание по имени (1000k).
	ContextWindow int `json:"context_window,omitempty"`
	// W15 этап 3: входные модальности пула (пересечение input_modalities всех
	// серверов) — узел пишет в settings.model.generationConfig.modalities
	// ({"image":bool,"video":bool}), чтобы qwen разрешал read_file медиа.
	InputModalities []string `json:"input_modalities,omitempty"`

	// node_health (14.4): {tmux_version, qwen_path, qwen_version,
	// disk_free_mb, time}. TmuxVersion/QwenPath/QwenVersion/DiskFreeMB — те же
	// поля, что и hello (tmux_version пуст — tmux нет, N9).
	DiskFreeMB  int    `json:"disk_free_mb,omitempty"`
	QwenPath    string `json:"qwen_path,omitempty"`
	QwenVersion string `json:"qwen_version,omitempty"`
	// N10: время узла (ms epoch) для индикации расхождения с часами координатора.
	NodeTimeMS int64 `json:"node_time_ms,omitempty"`

	// agent_exit (7.3, C1): код завершения кодера из @runpilot_exit (0 — штатно,
	// >128 — сигнал; узел парсит опцию панели). SID — из @runpilot_sid.
	ExitCode int `json:"exit_code,omitempty"`

	// pty_open / pty_close / pty_exit (13.4 ТЗ): Chan — номер PTY-канала
	// (двоичные кадры несут его в заголовке); TmuxSession — имя tmux-сессии,
	// к которой узел подключает tmux attach.
	Chan        int    `json:"chan,omitempty"`
	TmuxSession string `json:"tmux_session,omitempty"`
	Socket      string `json:"socket,omitempty"`
}

// EncodeFrame — двоичный кадр PTY-канала: номер канала (ptyChanLen байт,
// big-endian) + данные (13.4 ТЗ, раздел 15.5: «двоичный кадр — номер канала +
// данные PTY»).
func EncodeFrame(chan_ int, data []byte) []byte {
	out := make([]byte, ptyChanLen+len(data))
	for i := 0; i < ptyChanLen; i++ {
		out[i] = byte(chan_ >> (bitsPerByte * (ptyChanLen - 1 - i)))
	}
	copy(out[ptyChanLen:], data)
	return out
}

// DecodeFrame разбирает двоичный кадр: (номер канала, данные). Если данных
// меньше заголовка — (0, nil).
func DecodeFrame(frame []byte) (int, []byte) {
	if len(frame) < ptyChanLen {
		return 0, nil
	}
	var chan_ int
	for i := 0; i < ptyChanLen; i++ {
		chan_ = chan_<<bitsPerByte | int(frame[i])
	}
	return chan_, frame[ptyChanLen:]
}

// Pane — состояние управляемой панели (node → coordinator).
type Pane struct {
	PaneID       string `json:"pane_id"`
	SID          string `json:"sid,omitempty"`
	State        string `json:"state"`
	Hash         uint64 `json:"hash"`
	StableSince  string `json:"stable_since"`            // RFC3339, часы узла
	InputPreview string `json:"input_preview,omitempty"` // первые 80 символов
	InputEmpty   bool   `json:"input_empty"`
	AgentVersion string `json:"agent_version,omitempty"`
	// Терминал (13.4 ТЗ): tmux-сессия и сокет панели — для pty_open.
	Session string `json:"tmux_session,omitempty"`
	Socket  string `json:"socket,omitempty"`
}

// GPUCard — карта из телеметрии (Э6).
type GPUCard struct {
	Index       int `json:"index"`
	UtilPercent int `json:"util_percent"`
	TempC       int `json:"temp_c"`
	PowerW      int `json:"power_w"`
}

// ExternalProc — внешний кодер вне tmux/runpilot (8.2 ТЗ). Node шлёт source
// (цепочка родителей) и OpenAIBaseURL из /proc environ; координатор по ним
// выводит target (gateway:<sid> / server:<name> / unknown). Значения аргументов
// не сохраняются (R5) — только exe и имена флагов.
type ExternalProc struct {
	PID           int      `json:"pid"`
	PPID          int      `json:"ppid"`
	UID           int      `json:"uid"`
	StartSec      int64    `json:"start_sec"` // etimes (секунд с запуска)
	Exe           string   `json:"exe"`
	Flags         []string `json:"flags,omitempty"`
	Source        string   `json:"source"` // cron|systemd|sshd|tmux|other
	OpenAIBaseURL string   `json:"openai_base_url,omitempty"`
	CWD           string   `json:"cwd,omitempty"` // readlink /proc/<pid>/cwd (для команды перезапуска)
}

// ExecRoot — EXEC-корень сессии (PID процесса runpilot exec + время его
// старта, поле 22 /proc/<pid>/stat + sid). Кодекс правила «кодер вне очереди»:
// кодер управляем, если он потомок PID и время старта корневого процесса
// совпадает с Starttime (защита от переиспользования PID).
type ExecRoot struct {
	PID       int    `json:"pid"`
	Starttime int64  `json:"start_time"`
	SID       string `json:"sid"`
	PGID      int    `json:"pgid,omitempty"` // PGID кодера (kill -PGID SIGKILL)
}

// UnmanagedPane — панель с кодером, запущенным не через runpilot (8.3 X1):
// кандидат на «Перезапустить под runpilot» (adopt). Session/Dir — для карточки
// в вебе; Cmd — текущая команда панели.
type UnmanagedPane struct {
	PaneID  string `json:"pane_id"`
	Session string `json:"session"`
	Dir     string `json:"dir,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
	Socket  string `json:"socket,omitempty"`
}

// New — сообщение с корректной версией.
func New(kind Kind) Msg { return Msg{Type: kind, Proto: Version} }

// PaneOf — обёртка для сообщений pane.
func PaneOf(p Pane) Msg {
	m := New(KindPane)
	m.Pane = &p
	return m
}

// Acceptable — верна ли мажорная версия для координатора Version
// (совместимость: N принимает N и N−1; при N=1 допустима только 1).
func Acceptable(p int) bool {
	return p == Version || p == Version-1
}

// Valid — type из известного набора.
func (m Msg) Valid() error {
	if !AllKinds[m.Type] {
		return fmt.Errorf("proto: неизвестный type %q", m.Type)
	}
	if m.Proto != Version {
		return fmt.Errorf("proto: неизвестная версия %d", m.Proto)
	}
	return nil
}

// StableSince — RFC3339-запись времени для поля stable_since
// (RFC3339Nano: с дробной секундой, иначе задержка снимка
// квантуется до секунды и приёмочный замер по received_at
// теряет точность).
func StableSince(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
