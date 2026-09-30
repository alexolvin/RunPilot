package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"runpilot/internal/monitor"
)

// httpDo — точка подмены в тестах.
var httpDo = func(c *http.Client, url string) (*http.Response, error) {
	return c.Get(url)
}

// foundList — список найденных vllm: имён (первые 5 + всего).
func foundList(found []string) string {
	if len(found) == 0 {
		return "нет"
	}
	if len(found) > foundListMax {
		return strings.Join(found[:foundListMax], ", ") + fmt.Sprintf(" (… всего %d)", len(found))
	}
	return strings.Join(found, ", ")
}

// checkMetrics — по серверам (раздел 10 ТЗ): достижимость metrics_url и
// presence required vllm: метрик. Отсутствие → WARN (не молчаливый
// ext=0) со списком найденных vllm: имён.
func checkMetrics(ctx *Context) Result {
	if ctx.Cfg == nil || len(ctx.Cfg.Servers) == 0 {
		return result("metrics", PASS, "серверов в конфигурации нет")
	}
	cl := &http.Client{
		Timeout: time.Duration(ctx.Cfg.Monitor.MetricsTimeoutSec) * time.Second,
	}
	var warns []string
	okCount := 0
	for _, s := range ctx.Cfg.Servers {
		resp, err := httpDo(cl, s.MetricsURL)
		if err != nil {
			warns = append(warns,
				fmt.Sprintf("%s: %s недоступен (metrics_missing)", s.Name, s.MetricsURL))
			continue
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || rerr != nil {
			warns = append(warns,
				fmt.Sprintf("%s: /metrics → %d (metrics_missing)", s.Name, resp.StatusCode))
			continue
		}
		m, perr := monitor.ParseVLLMMetrics(body)
		if perr != nil {
			warns = append(warns, fmt.Sprintf("%s: /metrics не разобран: %v", s.Name, perr))
			continue
		}
		if miss := m.Missing(); len(miss) > 0 {
			warns = append(warns, fmt.Sprintf("%s: отсутствуют %s (найдены: %s)",
				s.Name, strings.Join(miss, ", "), foundList(m.Found)))
			continue
		}
		okCount++
	}
	if len(warns) > 0 {
		return result("metrics", WARN, strings.Join(warns, "; "))
	}
	return result("metrics", PASS,
		fmt.Sprintf("%d серверов: все vllm: метрики на месте", okCount))
}

// checkRequireDirect — servers[].require_direct (приложение В ТЗ):
// апстрим обязан быть одним vLLM — без редиректов и с единственной
// моделью. Нарушение — WARN (прокси обходит аренду).
func checkRequireDirect(ctx *Context) Result {
	if ctx.Cfg == nil {
		return result("require_direct", PASS, "нет конфигурации")
	}
	cl := &http.Client{
		Timeout: time.Duration(ctx.Cfg.Monitor.MetricsTimeoutSec) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var checked int
	var warns []string
	for _, s := range ctx.Cfg.Servers {
		if !s.RequireDirect {
			continue
		}
		checked++
		url := strings.TrimRight(s.Upstreams.OpenAI.URL, "/") + "/v1/models"
		resp, err := httpDo(cl, url)
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %s: %v", s.Name, url, err))
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= statusRedirectLow && resp.StatusCode < statusRedirectHigh {
			warns = append(warns, fmt.Sprintf("%s: /v1/models → %d (редирект — не один vLLM)",
				s.Name, resp.StatusCode))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			warns = append(warns, fmt.Sprintf("%s: /v1/models → %d", s.Name, resp.StatusCode))
			continue
		}
		var models struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &models); err != nil {
			warns = append(warns,
				fmt.Sprintf("%s: /v1/models не JSON: %v", s.Name, err))
			continue
		}
		want := s.Upstreams.OpenAI.Model
		if len(models.Data) != 1 {
			warns = append(warns, fmt.Sprintf("%s: %d моделей на апстриме (ожидался ровно 1: %s)",
				s.Name, len(models.Data), want))
			continue
		}
		if want != "" && models.Data[0].ID != want {
			warns = append(warns, fmt.Sprintf("%s: модель на апстриме %s, ожидаем %s",
				s.Name, models.Data[0].ID, want))
			continue
		}
	}
	if checked == 0 {
		return result("require_direct", PASS, "серверов с require_direct нет")
	}
	if len(warns) > 0 {
		return result("require_direct", WARN, strings.Join(warns, "; "))
	}
	return result("require_direct", PASS,
		fmt.Sprintf("%d серверов: один vLLM на апстриме", checked))
}
