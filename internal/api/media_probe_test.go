package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// M1: inOrder — все числа есть в s и идут по порядку.
func TestInOrder(t *testing.T) {
	cases := []struct {
		s    string
		nums []string
		want bool
	}{
		{"42,17,88", []string{"42", "17", "88"}, true},
		{"числа 42 потом 17 потом 88", []string{"42", "17", "88"}, true},
		{"88,17,42", []string{"42", "17", "88"}, false}, // не по порядку
		{"42,88", []string{"42", "17", "88"}, false},    // отсутствует 17
		{"1742 88", []string{"42", "17", "88"}, false},  // 42 до 17 → порядок нарушен
	}
	for _, c := range cases {
		if got := inOrder(c.s, c.nums); got != c.want {
			t.Errorf("inOrder(%q, %v) = %v, хочу %v", c.s, c.nums, got, c.want)
		}
	}
}

// M2: stripBase64 — base64-хвост (64+ символов) заменяется, короткое — нет.
func TestStripBase64(t *testing.T) {
	long := strings.Repeat("A", 200)
	in := `{"url":"data:image/png;base64,` + long + `"}`
	out := stripBase64([]byte(in))
	if strings.Contains(out, long) {
		t.Fatalf("base64 не удалён: %q", out)
	}
	if !strings.Contains(out, "[omitted]") {
		t.Fatalf("нет маркера [omitted]: %q", out)
	}
	// Короткая строка (не base64-хвост) остаётся.
	short := `{"text":"hello"}`
	if got := stripBase64([]byte(short)); got != short {
		t.Fatalf("короткое тело изменено: %q", got)
	}
}

// M3: evalStatus — HTTP 200 = PASS, иначе FAIL с телом.
func TestEvalStatus(t *testing.T) {
	if r := evalStatus(http.StatusOK, []byte(`{}`)); !r.OK {
		t.Fatalf("HTTP 200 должен быть PASS")
	}
	r := evalStatus(http.StatusRequestEntityTooLarge, []byte(`{"error":"too large"}`))
	if r.OK || r.HTTPStatus != http.StatusRequestEntityTooLarge {
		t.Fatalf("413 должен быть FAIL со статусом: %+v", r)
	}
	if r.VLLMBody != `{"error":"too large"}` {
		t.Fatalf("тело vLLM не записано: %+v", r)
	}
}

// M4: evalProbe — HTTP 200 + проверка контента.
func TestEvalProbe(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"на картинке 123456"}}]}`)
	r := evalProbe(http.StatusOK, body, func(c string) (bool, string) {
		return strings.Contains(c, "123456"), "ок"
	})
	if !r.OK {
		t.Fatalf("число найдено, должно быть PASS: %+v", r)
	}
	// Число не найдено → FAIL с телом.
	r = evalProbe(http.StatusOK, body, func(c string) (bool, string) {
		return strings.Contains(c, "999999"), "нет"
	})
	if r.OK {
		t.Fatalf("число не найдено, должно быть FAIL")
	}
	// Не-200 → FAIL со статусом.
	if r := evalProbe(http.StatusBadGateway, []byte(`{}`), func(c string) (bool, string) { return true, "" }); r.OK {
		t.Fatalf("502 должно быть FAIL")
	}
}

// M5: mediaProbeAllowed — установка модальности только по PASS.
func TestMediaProbeAllowed(t *testing.T) {
	s := &Server{}
	// Пробы не выполнялись → отклонение.
	if err := s.mediaProbeAllowed("srv", nil, []string{"image"}); err == nil {
		t.Fatalf("без проб должно быть отклонение")
	}
	// Пробы PASS → установка разрешена.
	s.setMediaProbeResult("srv", &mediaProbeResult{
		Server: "srv",
		Image:     probeTest{OK: true, HTTPStatus: 200},
		ManyImage: probeTest{OK: true, HTTPStatus: 200},
		Video:     probeTest{OK: false, HTTPStatus: 200, Detail: "no"},
		ManyVideo: probeTest{OK: true, HTTPStatus: 200},
	})
	if err := s.mediaProbeAllowed("srv", nil, []string{"image"}); err != nil {
		t.Fatalf("image PASS, установка должна быть разрешена: %v", err)
	}
	// video FAIL (P-video) → отклонение с ответом.
	err := s.mediaProbeAllowed("srv", nil, []string{"video"})
	if err == nil {
		t.Fatalf("video FAIL, установка должна быть отклонена")
	}
	var f *mediaProbeFailure
	if !errors.As(err, &f) || f.Modality != "video" {
		t.Fatalf("ожидалась mediaProbeFailure(video): %v", err)
	}
	// Уже установленная модальность — повторное сохранение без PASS.
	if err := s.mediaProbeAllowed("srv", []string{"image"}, []string{"image"}); err != nil {
		t.Fatalf("уже установленная image не требует PASS: %v", err)
	}
}
