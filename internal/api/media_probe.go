package api

// Медиа-пробы (цикл W15, этап 2): проверка входных модальностей сервера.
//
//   - P-image:      PNG со случайным 6-значным числом → ответ содержит число.
//   - P-video:      6-с mp4 с тремя 2-значными числами (по 2 с) → ответ
//     содержит все три по порядку.
//   - P-many-image: N картинок в одном запросе → HTTP 200 (N = media.
//     probe_many_images = screenshotTriggerThreshold Qwen Code).
//   - P-many-video: 2 видео в одном запросе → HTTP 200.
//
// Пул-сервер — через шлюз: сессия с пином на сервер, шлюз выдаёт неявную
// аренду пин-сервера на первом генеративном запросе (аренда = lease).
// Генерация медиа — ffmpeg drawtext (DejaVu); чисел в кадре — случайные.
// Ответ vLLM на FAIL записывается со статусом и телом без base64.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"runpilot/internal/model"
	"runpilot/internal/sid"
	"runpilot/internal/store"
)

// probeTest — результат одной медиа-пробы.
type probeTest struct {
	OK         bool   `json:"ok"`
	HTTPStatus int    `json:"http_status"`
	Detail     string `json:"detail"`
	// VLLMBody — тело ответа vLLM на FAIL (без base64); пусто на PASS.
	VLLMBody string `json:"vllm_body,omitempty"`
}

// mediaProbeResult — результат медиа-проб сервера (по модальности: одиночная
// + «много» в одном запросе). Modality PASS = обе пробы OK.
type mediaProbeResult struct {
	Server    string    `json:"server"`
	Image     probeTest `json:"image"`
	ManyImage probeTest `json:"many_image"`
	Video     probeTest `json:"video"`
	ManyVideo probeTest `json:"many_video"`
	At        string    `json:"at"`
}

// imagePass / videoPass — модальность доступна, если PASS обе пробы.
func (r *mediaProbeResult) imagePass() bool { return r.Image.OK && r.ManyImage.OK }
func (r *mediaProbeResult) videoPass() bool { return r.Video.OK && r.ManyVideo.OK }

// mediaProbeFailure — ответ vLLM, блокирующий установку модальности.
type mediaProbeFailure struct {
	Modality   string
	HTTPStatus int
	Body       string
}

func (e *mediaProbeFailure) Error() string {
	return "модальность " + e.Modality + " не установлена (нет PASS): " + e.Body
}

// randDigits — случайное число из range цифр (min + rand.Intn(range)).
func randDigits(min, rng int) int { return min + rand.Intn(rng) }

// genProbePNG — кадр PNG со числом (ffmpeg drawtext). Возвращает путь.
func genProbePNG(dir, num string) (string, error) {
	path := filepath.Join(dir, "probe.png")
	vf := fmt.Sprintf("drawtext=text=%s:fontfile=%s:fontcolor=black:fontsize=%d:x=(w-tw)/2:y=(h-th)/2",
		num, mediaProbeFontPath, mediaProbeFontSize)
	args := []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=white:s=%dx%d", mediaProbePNGW, mediaProbePNGH),
		"-vf", vf, "-frames:v", "1", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg png: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// genProbeMP4 — mp4 с числами (по mediaProbeSegSec на сегмент, ffmpeg
// drawtext + enable). Чисел = len(nums) = mediaProbeNumSegs.
func genProbeMP4(dir string, nums []string) (string, error) {
	path := filepath.Join(dir, "probe.mp4")
	f := mediaProbeFontPath
	fs := mediaProbeFontSize
	segs := make([]string, 0, len(nums))
	for i, n := range nums {
		a, b := i*mediaProbeSegSec, (i+1)*mediaProbeSegSec
		segs = append(segs, fmt.Sprintf(
			"drawtext=text=%s:fontfile=%s:fontcolor=black:fontsize=%d:x=(w-tw)/2:y=(h-th)/2:enable='between(t,%d,%d)'",
			n, f, fs, a, b))
	}
	args := []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("color=white:s=%dx%d", mediaProbePNGW, mediaProbePNGH),
		"-t", strconv.Itoa(mediaProbeMP4Sec), "-vf", strings.Join(segs, ","),
		"-r", strconv.Itoa(mediaProbeMP4FPS), "-pix_fmt", "yuv420p", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg mp4: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return path, nil
}

// dataURI — base64-контейнер для медиа в OpenAI-запросе.
func dataURI(path, mime string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// mediaProbeBody — тело chat/completions: текст + список медиа-частей.
func mediaProbeBody(modelAlias string, text string, parts []any) []byte {
	content := append([]any{map[string]any{"type": "text", "text": text}}, parts...)
	body := map[string]any{
		"model":      modelAlias,
		"messages":   []any{map[string]any{"role": "user", "content": content}},
		"max_tokens": mediaProbeMaxTokens,
	}
	b, _ := json.Marshal(body)
	return b
}

// imagePart / videoPart — одна медиа-часть в content.
func imagePart(uri string) any {
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}}
}
func videoPart(uri string) any {
	return map[string]any{"type": "video_url", "video_url": map[string]any{"url": uri}}
}

// probeChat — сессия с пином на сервер + генеративный запрос через шлюз.
// Шлюз выдаёт неявную аренду пин-сервера (аренда) и проксирует. Возвращает
// статус и тело ответа vLLM. Сессия убирается (аренда TURN_DONE, GONE).
func (s *Server) probeChat(serverName string, body []byte) (int, []byte, error) {
	si, err := sid.New()
	if err != nil {
		return 0, nil, err
	}
	now := s.clk.Now()
	rec := store.SessionRecord{
		SID: si, Name: "mediaprobe-" + si, Host: "", HostIP: loopbackIP,
		Profile: "qwen", Kind: model.KindJob,
		State: model.SessionIdle, StateChangedAt: now,
		Class: model.ClassHigh,
		ConstraintKind:   model.ConstraintPin, ConstraintServer: serverName,
		CreatedAt: now,
	}
	if err := s.store.CreateSession(rec); err != nil {
		return 0, nil, err
	}
	defer s.cleanupProbeSession(si)

	url := s.cfg.Coordinator.GatewayURL + "/s/" + si + "/v1/chat/completions"
	ctx, cancel := context.WithTimeout(context.Background(), mediaProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer runpilot")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

// cleanupProbeSession — снять аренду + GONE (как завершение JOB).
func (s *Server) cleanupProbeSession(si string) {
	s.releaseOnGone(si, model.ReleaseTurnDone)
	_ = s.store.QueueDelete(si)
	_ = s.store.JobDelete(si)
	_ = s.store.SetSessionState(si, model.SessionGone, "", s.clk.Now())
}

// chatContent — choices[0].message.content из ответа chat/completions.
func chatContent(respBody []byte) string {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(respBody, &parsed) != nil || len(parsed.Choices) == 0 {
		return ""
	}
	return parsed.Choices[0].Message.Content
}

// stripBase64 — убрать base64-контейнеры из тела (артефакт без медиа):
// data:...;base64,<64+ символов> → data:...;base64,[omitted].
func stripBase64(body []byte) string {
	return base64StripRE.ReplaceAllString(string(body), "[omitted]")
}

// base64StripRE — хвост data-URI (64+ символов base64).
var base64StripRE = regexp.MustCompile(`[A-Za-z0-9+/=]{64,}`)

// runMediaProbes — все четыре пробы сервера (через шлюз, аренда пин-сервера).
// Возвращает результат + ошибку генерации медиа (пробы не выполнены).
func (s *Server) runMediaProbes(serverName string) (*mediaProbeResult, error) {
	dir, err := os.MkdirTemp("", "runpilot-media-probe-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	num6 := strconv.Itoa(randDigits(mediaProbeNum6Min, mediaProbeNum6Range))
	nums := make([]string, 0, mediaProbeNumSegs)
	for i := 0; i < mediaProbeNumSegs; i++ {
		nums = append(nums, strconv.Itoa(randDigits(mediaProbeNum2Min, mediaProbeNum2Range)))
	}
	png, err := genProbePNG(dir, num6)
	if err != nil {
		return nil, err
	}
	mp4, err := genProbeMP4(dir, nums)
	if err != nil {
		return nil, err
	}
	pngURI, err := dataURI(png, "image/png")
	if err != nil {
		return nil, err
	}
	mp4URI, err := dataURI(mp4, "video/mp4")
	if err != nil {
		return nil, err
	}
	alias := s.cfg.Profiles.Qwen.ModelAlias

	res := &mediaProbeResult{Server: serverName, At: s.clk.Now().UTC().Format("2006-01-02T15:04:05Z")}

	// P-image: одна картинка → ответ содержит число.
	st, body, _ := s.probeChat(serverName, mediaProbeBody(alias, "Какое число на картинке?", []any{imagePart(pngURI)}))
	res.Image = evalProbe(st, body, func(c string) (bool, string) {
		if strings.Contains(c, num6) {
			return true, "найдено " + num6
		}
		return false, "число " + num6 + " не найдено"
	})
	// P-many-image: N картинок → HTTP 200.
	parts := make([]any, 0, s.cfg.Media.ProbeManyImages)
	for i := 0; i < s.cfg.Media.ProbeManyImages; i++ {
		parts = append(parts, imagePart(pngURI))
	}
	st, body, _ = s.probeChat(serverName, mediaProbeBody(alias, "Опиши картинки.", parts))
	res.ManyImage = evalStatus(st, body)
	// P-video: одно видео → ответ содержит все числа по порядку.
	numsStr := strings.Join(nums, ",")
	st, body, _ = s.probeChat(serverName, mediaProbeBody(alias, "Назови три числа на видео по порядку.", []any{videoPart(mp4URI)}))
	res.Video = evalProbe(st, body, func(c string) (bool, string) {
		if inOrder(c, nums) {
			return true, "найдены " + numsStr
		}
		return false, "числа " + numsStr + " не найдены по порядку"
	})
	// P-many-video: 2 видео → HTTP 200.
	st, body, _ = s.probeChat(serverName, mediaProbeBody(alias, "Опиши видео.", []any{videoPart(mp4URI), videoPart(mp4URI)}))
	res.ManyVideo = evalStatus(st, body)
	return res, nil
}

// evalProbe — генеративная проба: PASS если HTTP 200 и проверка контента OK.
func evalProbe(status int, body []byte, check func(string) (bool, string)) probeTest {
	if status != http.StatusOK {
		return probeTest{OK: false, HTTPStatus: status, Detail: "HTTP "+strconv.Itoa(status), VLLMBody: stripBase64(body)}
	}
	ok, detail := check(chatContent(body))
	if !ok {
		return probeTest{OK: false, HTTPStatus: status, Detail: detail, VLLMBody: stripBase64(body)}
	}
	return probeTest{OK: true, HTTPStatus: status, Detail: detail}
}

// evalStatus — проба «только HTTP 200» (P-many-*).
func evalStatus(status int, body []byte) probeTest {
	if status == http.StatusOK {
		return probeTest{OK: true, HTTPStatus: status, Detail: "HTTP 200"}
	}
	return probeTest{OK: false, HTTPStatus: status, Detail: "HTTP "+strconv.Itoa(status), VLLMBody: stripBase64(body)}
}

// inOrder — все nums есть в s и идут по порядку.
func inOrder(s string, nums []string) bool {
	pos := -1
	for _, n := range nums {
		idx := strings.Index(s[pos+1:], n)
		if idx < 0 {
			return false
		}
		pos += idx
	}
	return true
}

// handleMediaProbe — POST /api/v1/servers/{name}/media-probe: все четыре
// пробы через шлюз (аренда пин-сервера). Результат хранится для проверки
// установки модальностей (PATCH input_modalities).
func (s *Server) handleMediaProbe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.store.GetServer(name); err != nil {
		httpError(w, http.StatusNotFound, "NOT_FOUND", "нет сервера")
		return
	}
	res, err := s.runMediaProbes(name)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "PROBE", err.Error())
		return
	}
	s.setMediaProbeResult(name, res)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// --- Хранение результатов проб (память координатора) ---

func (s *Server) setMediaProbeResult(name string, res *mediaProbeResult) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if s.mediaProbes == nil {
		s.mediaProbes = map[string]*mediaProbeResult{}
	}
	s.mediaProbes[name] = res
}

// mediaProbeAllowed — разрешена ли установка модальности по PASS проб.
// Возвращает ошибку с ответом vLLM (без base64) на проваленную пробу.
func (s *Server) mediaProbeAllowed(name string, current, wanted []string) error {
	cur := map[string]bool{}
	for _, m := range current {
		cur[m] = true
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	res, ok := s.mediaProbes[name]
	if !ok {
		return &mediaProbeFailure{Modality: "любая", HTTPStatus: 0,
			Body: "медиа-пробы не выполнялись: выполните POST /api/v1/servers/" + name + "/media-probe"}
	}
	for _, m := range wanted {
		if cur[m] {
			continue // уже установлена — повторное сохранение не требует PASS
		}
		switch m {
		case "image":
			if !res.imagePass() {
				return &mediaProbeFailure{Modality: "image",
					HTTPStatus: firstFailStatus(res.Image, res.ManyImage),
					Body: probeFailBody(res.Image, res.ManyImage)}
			}
		case "video":
			if !res.videoPass() {
				return &mediaProbeFailure{Modality: "video",
					HTTPStatus: firstFailStatus(res.Video, res.ManyVideo),
					Body: probeFailBody(res.Video, res.ManyVideo)}
			}
		}
	}
	return nil
}

func firstFailStatus(a, b probeTest) int {
	if !a.OK && a.HTTPStatus != 0 {
		return a.HTTPStatus
	}
	return b.HTTPStatus
}

func probeFailBody(a, b probeTest) string {
	var parts []string
	for _, t := range []probeTest{a, b} {
		if t.OK {
			continue
		}
		p := t.Detail
		if t.VLLMBody != "" {
			p += ": " + t.VLLMBody
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " | ")
}
