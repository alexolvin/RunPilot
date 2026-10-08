package doctor

// linger — узел живёт как systemd user-сервис; без linger user@UID умирает
// при logout. Тесты: Linger=yes → PASS; Linger=no → FAIL с командой включения;
// ошибка loginctl → FAIL; роль node содержит проверку linger.

import (
	"errors"
	"strings"
	"testing"
)

func TestLingerPass(t *testing.T) {
	currentUser = func() (string, error) { return "alice", nil }
	loginctlShowUser = func(string) ([]byte, error) {
		return []byte("Linger=yes\n..."), nil
	}
	if r := checkLinger(nil); r.Level != PASS {
		t.Fatalf("Linger=yes: %s", r)
	}
}

func TestLingerFail(t *testing.T) {
	currentUser = func() (string, error) { return "alice", nil }
	loginctlShowUser = func(string) ([]byte, error) {
		return []byte("Linger=no\n..."), nil
	}
	r := checkLinger(nil)
	if r.Level != FAIL || !strings.Contains(r.Detail, "linger не включён") {
		t.Fatalf("Linger=no: %s", r)
	}
	if !strings.Contains(r.Detail, "systemctl --enable-linger alice") {
		t.Fatalf("нет команды включения: %s", r)
	}
}

func TestLingerLoginctlError(t *testing.T) {
	currentUser = func() (string, error) { return "alice", nil }
	loginctlShowUser = func(string) ([]byte, error) {
		return nil, errors.New("not found")
	}
	r := checkLinger(nil)
	if r.Level != FAIL || !strings.Contains(r.Detail, "loginctl") {
		t.Fatalf("ошибка loginctl: %s", r)
	}
}

func TestNodeRoleHasLinger(t *testing.T) {
	for _, c := range RoleChecks(RoleNode) {
		if c.Name == "linger" {
			return
		}
	}
	t.Fatal("роль node не содержит проверки linger")
}
