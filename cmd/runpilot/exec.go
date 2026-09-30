// Раздел 8.1 ТЗ: runpilot exec — запуск задания из cron под управлением runpilot.
//
// runpilot exec [--name N] [--dir PATH] [--prio C] [--pin S|--prefer S]
//          [--wait-max SEC] [--timeout SEC] -- <аргументы qwen>
//
// Коды выхода: 69 (координатор недоступен), 75 (слот не получен за
// --wait-max), 77 (отказ в доступе), 124 (истёк --timeout).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"runpilot/profiles"
)

type execOpts struct {
	name    string
	dir     string
	prio    string
	pin     string
	prefer  string
	waitMax int // сек; 0 → jobs.wait_max_default_sec
	timeout int // сек; 0 → без таймаута
	args    []string
}

func newExecCmd() *cobra.Command {
	var o execOpts
	cmd := &cobra.Command{
		Use:   "exec [--name N] [--dir PATH] [--prio C] [--pin S|--prefer S] [--wait-max SEC] [--timeout SEC] -- <аргументы qwen>",
		Short: "Запустить задание (cron) под управлением runpilot: аренда слота + окружение шлюза",
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.pin != "" && o.prefer != "" {
				return fmt.Errorf("--pin и --prefer взаимоисключающие")
			}
			code, err := runExec(o)
			if code != 0 {
				// Кода выхода не 0 — печатаем причину в stderr и выходим кодом.
				if err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "runpilot exec:", err)
				}
			}
			os.Exit(code)
			return nil
		},
	}
	cmd.Flags().StringVar(&o.name, "name", "", "имя задания (по умолчанию job-<sid>)")
	cmd.Flags().StringVar(&o.dir, "dir", ".", "рабочий каталог")
	cmd.Flags().StringVar(&o.prio, "prio", "normal", "класс очереди: resume|high|normal|low")
	cmd.Flags().StringVar(&o.pin, "pin", "", "привязка к серверу")
	cmd.Flags().StringVar(&o.prefer, "prefer", "", "предпочтение сервера")
	cmd.Flags().IntVar(&o.waitMax, "wait-max", 0, "макс. ожидание слота, сек (0 = jobs.wait_max_default_sec)")
	cmd.Flags().IntVar(&o.timeout, "timeout", 0, "макс. время задания, сек (0 = без ограничения)")
	return cmd
}

// runExec — полный сценарий runpilot exec (раздел 8.1). Возвращает код выхода.
func runExec(o execOpts) (int, error) {
	prof, err := profiles.LoadQwen()
	if err != nil {
		return exitConfigError, err
	}
	token := os.Getenv(cfg.Client.TokenEnv)
	api := &jobAPI{
		base:   cfg.Client.Coordinator,
		token:  token,
		client: &http.Client{}, // без таймаута: long-poll держится wait_sec
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) Регистрация (п.2).
	sid, err := api.create(ctx, o.name, o.prio, o.pin, o.prefer)
	if err != nil {
		return jobExitCode(err), err
	}

	// Очистка точно один раз (аренда/очередь/запись).
	finished := false
	finishOnce := func(code int, signal string) {
		if finished {
			return
		}
		finished = true
		_ = api.finish(context.Background(), sid, code, signal)
	}
	defer finishOnce(0, "")

	// 2) Ожидание аренды (п.3): long-poll, сумма не больше wait-max.
	waitMax := o.waitMax
	if waitMax <= 0 {
		waitMax = cfg.Jobs.WaitMaxDefaultSec
	}
	deadline := time.Now().Add(time.Duration(waitMax) * time.Second)
	var env map[string]string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			finishOnce(exitNoSlot, "")
			return exitNoSlot, fmt.Errorf("слот не получен за %d с", waitMax)
		}
		poll := jobPollSec
		if r := int(remaining.Seconds()); r < poll {
			poll = r
			if poll < 1 {
				poll = 1
			}
		}
		res, granted, done, werr := api.wait(ctx, sid, poll)
		if werr != nil {
			return jobExitCode(werr), werr
		}
		if granted {
			env = res.Env
			break
		}
		if done {
			// Сессия завершена (GONE/HOLD) — ожидание бессмысленно.
			return exitNoSlot, fmt.Errorf("сессия %s завершена до выдачи аренды", sid)
		}
	}

	// 3) Запуск qwen (п.4): stdin=/dev/null, stdout/stderr — наследуемые.
	cmd, err := buildQwenCmd(prof.Command, o.args, env)
	if err != nil {
		finishOnce(exitConfigError, "")
		return exitConfigError, err
	}
	if err := cmd.Start(); err != nil {
		finishOnce(1, "")
		return 1, fmt.Errorf("запуск %s: %w", prof.Command, err)
	}

	// 5) Пульсы (п.5) + 6) --timeout (п.6): конкурентно с ожиданием выхода.
	killGrace := time.Duration(cfg.Turn.KillGraceSec) * time.Second
	if killGrace <= 0 {
		killGrace = defKillGraceSec * time.Second
	}
	hbEvery := time.Duration(cfg.Jobs.HeartbeatSec) * time.Second
	if hbEvery <= 0 {
		hbEvery = defHeartbeatSec * time.Second
	}
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	// Пульс-горутина: при cancel (аварийная/«Прервать») сигналит в cancelCh.
	cancelCh := make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(hbEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c, herr := api.heartbeat(ctx, sid)
				if herr == nil && c {
					select {
					case cancelCh <- struct{}{}:
					default:
					}
					return
				}
			}
		}
	}()

	var timeoutCh <-chan time.Time
	if o.timeout > 0 {
		timeoutCh = time.After(time.Duration(o.timeout) * time.Second)
	} else {
		timeoutCh = make(chan time.Time) // никогда
	}

	// kill: SIGTERM, через killGrace — SIGKILL.
	kill := func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			select {
			case <-doneCh:
			case <-time.After(killGrace):
				_ = cmd.Process.Kill()
			}
		}()
	}

	// Дождаться одного из: выход процесса / cancel / --timeout.
	var (
		procErr  error
		timedOut bool
	)
	select {
	case procErr = <-doneCh:
	case <-cancelCh:
		kill()
		procErr = <-doneCh
	case <-timeoutCh:
		timedOut = true
		kill()
		procErr = <-doneCh
	}

	code := exitResultOf(procErr)
	if timedOut {
		code = exitTimeout
	}
	finishOnce(code, signalNameOf(procErr))
	if timedOut {
		return exitTimeout, fmt.Errorf("истёк --timeout %d с", o.timeout)
	}
	if procErr != nil {
		return code, procErr
	}
	return 0, nil
}

// jobExitCode — классификация ошибки протокола в код выхода runpilot exec.
func jobExitCode(err error) int {
	switch {
	case errors.Is(err, errNoCoordinator):
		return exitNoCoordinator
	case errors.Is(err, errDenied):
		return exitDenied
	default:
		return 1
	}
}

// buildQwenCmd — процесс qwen с окружением шлюза (п.4).
func buildQwenCmd(command string, args []string, env map[string]string) (*exec.Cmd, error) {
	absDir, err := os.Getwd()
	if err != nil {
		absDir = ""
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = absDir
	inherit := os.Environ()
	for k, v := range env {
		inherit = append(inherit, k+"="+v)
	}
	cmd.Env = inherit
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		devNull = nil
	}
	cmd.Stdin = devNull
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Свой процесс-групп: SIGTERM/SIGKILL к группе при завершении.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd, nil
}

// exitResultOf — код выхода из результата cmd.Wait. Убитый сигналом
// процесс (ExitStatus = -1) → 1 (не «удачный» выход, но не таймаут).
func exitResultOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if c := ws.ExitStatus(); c >= 0 {
				return c
			}
			// Signaled → ExitStatus() == -1.
			return 1
		}
	}
	return 1
}

// signalNameOf — имя сигнала, убившего процесс ("" при обычном выходе).
func signalNameOf(err error) string {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return ""
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return strconv.Itoa(int(ws.Signal()))
	}
	return ""
}
