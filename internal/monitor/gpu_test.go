package monitor

import (
	"os"
	"testing"
)

// TestParseNvidiaReal — записанный вывод nvidia-smi (2 карты).
func TestParseNvidiaReal(t *testing.T) {
	body, err := os.ReadFile(fixturePath(t, "gpu", "nvidia_real.csv"))
	if err != nil {
		t.Fatal(err)
	}
	cards, err := ParseNvidia(body)
	if err != nil {
		t.Fatalf("реальная фикстура не разобрана: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("карт = %d, хочу 2", len(cards))
	}
	c0, c1 := cards[0], cards[1]
	// 0, 98, 23056, 24576, 65, 337.84
	if c0.Index != 0 || c0.UtilPercent != 98 || c0.TempC != 65 || c0.PowerW != 337 {
		t.Errorf("card0 = %+v", c0)
	}
	if c0.VRAMUsedGB != 23056/1024.0 || c0.VRAMTotalGB != 24576/1024.0 {
		t.Errorf("card0 vram = %v/%v GB, хочу %v/%v",
			c0.VRAMUsedGB, c0.VRAMTotalGB, 23056/1024.0, 24576/1024.0)
	}
	// 1, 100, 23056, 24576, 63, 325.40
	if c1.Index != 1 || c1.UtilPercent != 100 || c1.TempC != 63 || c1.PowerW != 325 {
		t.Errorf("card1 = %+v", c1)
	}
}

// TestParseROCmReal — фикстура amd_real.json (формат rocm-smi --json).
func TestParseROCmReal(t *testing.T) {
	body, err := os.ReadFile(fixturePath(t, "gpu", "amd_real.json"))
	if err != nil {
		t.Fatal(err)
	}
	cards, err := ParseROCm(body)
	if err != nil {
		t.Fatalf("реальная фикстура не разобрана: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("карт = %d, хочу 2", len(cards))
	}
	c0, c1 := cards[0], cards[1]
	// card0: 87%, 61C, 312.5W, 64 GiB total, 54 GiB used
	if c0.Index != 0 || c0.UtilPercent != 87 || c0.TempC != 61 || c0.PowerW != 312 {
		t.Errorf("card0 = %+v", c0)
	}
	if c0.VRAMTotalGB != 64 || c0.VRAMUsedGB != 54 {
		t.Errorf("card0 vram = %v/%v GB, хочу 64/54", c0.VRAMUsedGB, c0.VRAMTotalGB)
	}
	// card1: 0%, 44C, 95.2W, 64 GiB total, 4 GiB used
	if c1.Index != 1 || c1.UtilPercent != 0 || c1.TempC != 44 || c1.PowerW != 95 {
		t.Errorf("card1 = %+v", c1)
	}
	if c1.VRAMTotalGB != 64 || c1.VRAMUsedGB != 4 {
		t.Errorf("card1 vram = %v/%v GB, хочу 64/4", c1.VRAMUsedGB, c1.VRAMTotalGB)
	}
}

// TestParseROCmStrings — значения-строки (старые версии rocm-smi).
func TestParseROCmStrings(t *testing.T) {
	body := []byte(`{"card0":{"GPU use (%)":"50","GPU temp (degC)":"42","GPU power (watts)":"100.5","VRAM Total Memory (B)":"68719476736","VRAM Total Used Memory (B)":"1073741824"}}`)
	cards, err := ParseROCm(body)
	if err != nil {
		t.Fatal(err)
	}
	c := cards[0]
	if c.UtilPercent != 50 || c.TempC != 42 || c.PowerW != 100 {
		t.Errorf("card = %+v", c)
	}
	if c.VRAMTotalGB != 64 || c.VRAMUsedGB != 1 {
		t.Errorf("vram = %v/%v, хочу 64/1", c.VRAMUsedGB, c.VRAMTotalGB)
	}
}

// TestParseNvidiaRejects — битые строки → ошибка.
func TestParseNvidiaRejects(t *testing.T) {
	for _, bad := range []string{
		"",
		"0, 98",
		"x, 98, 1, 2, 3, 4",
		"No devices were found",
	} {
		if _, err := ParseNvidia([]byte(bad)); err == nil {
			t.Errorf("%q: хочу ошибку", bad)
		}
	}
}

// TestParseROCmRejects — не JSON / нет полей → ошибка.
func TestParseROCmRejects(t *testing.T) {
	for _, bad := range []string{
		"",
		`not json`,
		`{}`,
		`{"card0":{"GPU use (%)":50}}`,
	} {
		if _, err := ParseROCm([]byte(bad)); err == nil {
			t.Errorf("%s: хочу ошибку", bad)
		}
	}
}
