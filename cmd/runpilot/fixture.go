package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"runpilot/profiles"
)

// newFixtureCmd — `runpilot fixture capture <target> <state>` (раздел 7 ТЗ):
// снимает видимый экран tmux-панели и сохраняет его в
// testdata/fixtures/qwen/<version>/<state>-NN.txt.
func newFixtureCmd() *cobra.Command {
	var (
		fixDir  string
		socket  string
		version string
	)
	capture := &cobra.Command{
		Use:   "capture <target> <state>",
		Short: "Снять экран tmux-панели в testdata/fixtures/qwen/<version>/<state>-NN.txt",
		Args:  cobra.ExactArgs(twoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, state := args[0], args[1]
			if !stateRe.MatchString(state) {
				return fmt.Errorf("state %q: допустимы [a-z][a-z0-9_]*", state)
			}
			if version == "" {
				p, err := profiles.LoadQwen()
				if err != nil {
					return err
				}
				version, err = agentVersion(p.VersionCmd)
				if err != nil {
					return fmt.Errorf("определение версии агента: %w (укажите --version)", err)
				}
			}
			out, err := exec.Command("tmux", captureArgs(socket, target)...).Output()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					return fmt.Errorf("tmux capture-pane: %s", strings.TrimSpace(string(ee.Stderr)))
				}
				return fmt.Errorf("tmux capture-pane: %w", err)
			}
			dir := filepath.Join(fixDir, "qwen", version)
			if err := os.MkdirAll(dir, plainDirMode); err != nil {
				return err
			}
			path := filepath.Join(dir, fmt.Sprintf("%s-%02d.txt", state, nextFixtureSeq(dir, state)))
			if err := os.WriteFile(path, out, plainFileMode); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	capture.Flags().StringVar(&fixDir, "dir", "testdata/fixtures", "каталог фикстур (относительно cwd)")
	capture.Flags().StringVar(&socket, "socket", "", "tmux-сокет (-L)")
	capture.Flags().StringVar(&version, "version", "", "версия агента (по умолчанию — из version_cmd профиля)")
	fixture := &cobra.Command{Use: "fixture", Short: "Фикстуры экранов (раздел 7 ТЗ)"}
	fixture.AddCommand(capture)
	return fixture
}

var stateRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var seqRe = regexp.MustCompile(`^([a-z][a-z0-9_]*)-([0-9]+)\.txt$`)

// captureArgs — аргументы tmux для снимка видимого экрана
// (раздел 7 ТЗ: перенесённые строки склеены). Сокет — глобальный флаг tmux,
// поэтому он идёт перед подкомандой.
func captureArgs(socket, target string) []string {
	var args []string
	if socket != "" {
		args = append(args, "-L", socket)
	}
	return append(args, "capture-pane", "-p", "-J", "-t", target)
}

// agentVersion — версия агента из version_cmd (последний токен вывода).
func agentVersion(versionCmd string) (string, error) {
	parts := strings.Fields(versionCmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("version_cmd пуст")
	}
	out, err := exec.Command(parts[0], parts[1:]...).Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("version_cmd не вернул версию")
	}
	v := fields[len(fields)-1]
	if !versionRe.MatchString(v) {
		return "", fmt.Errorf("не похож на версию: %q", v)
	}
	return v, nil
}

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)

// nextFixtureSeq — следующий порядковый номер <state>-NN в каталоге.
func nextFixtureSeq(dir, state string) int {
	max := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	for _, e := range entries {
		m := seqRe.FindStringSubmatch(e.Name())
		if m == nil || m[1] != state {
			continue
		}
		n, err := strconv.Atoi(m[seqFieldIdx])
		if err == nil && n > max {
			max = n
		}
	}
	return max + 1
}
