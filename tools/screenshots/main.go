// Command screenshots — конвейер скриншотов (v2 раздел 19.2/19.3).
//
// Поднимает стенд (runpilot-stand) как подпроцесс (по одному на сценарий), снимает
// каждую комбинацию 19.2 (сценарий × страница × окно × тема) в headless Chrome
// (go-rod) и автоматически прогоняет проверки 19.3. Консоль/сеть наблюдаются
// injected-скриптом (без модификации сети). Любая находка — выход с кодом 1
// (make screenshots падает на регресс). Первый запуск создаёт golden-базу;
// последующие — сравнивают (diff ≤ --diff-max %).
//
// W3: вход + каркас (пустая очередь), префикс «empty».
// W4: 8 обязательных сценариев 19.1 × 7 страниц 11.x (1440×900 светлая) +
//
//	канонический normal во всех окнах/темах; offline — после обрыва SSE.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

type windowCfg struct {
	name   string // "1440x900"
	w, h   int
	scale  float64
	phone  bool
	themes []string
}

// Комбинации 19.2: 1440×900 (light,dark), 1024×768 (light), 390×844 (light,dark).
var windows = []windowCfg{
	{name: "1440x900", w: 1440, h: 900, scale: 1, themes: []string{"light", "dark"}},
	{name: "1024x768", w: 1024, h: 768, scale: 1, themes: []string{"light"}},
	{name: "390x844", w: 390, h: 844, scale: 3, phone: true, themes: []string{"light", "dark"}},
}

func windowByName(name string) windowCfg {
	for _, w := range windows {
		if w.name == name {
			return w
		}
	}
	return windows[0]
}

// combo — одна комбинация снимка: сценарий × страница × окно × тема.
// param — подмаршрут страницы (W5: состояние сессии для карточки session-card).
// journey — пользовательский сценарий J1/J2 (19.5): имя файла
// journey-J<n>-<шаг>__<окно>.png; состояние сессии ставится через /control/state.
type combo struct {
	scenario string
	page     string
	win      string
	theme    string
	param    string
	journey  string
	sessName string // W5: имя сессии для шага J1/J2 (навигация напрямую)
}

// w4Pages — страницы раздела 11 (W4).
var w4Pages = []string{"queue", "sessions", "servers", "nodes", "journal", "monitoring", "notifications"}

// w4Scenarios — обязательные сценарии 19.1.
var w4Scenarios = []string{"empty", "normal", "attention", "faults", "emergency", "safe_mode", "offline", "many"}

// combosFor — набор комбинаций снимка по этапу.
//   - W3: вход + пустая очередь во всех окнах/темах (префикс «empty»).
//   - W4 (и др.): все страницы 11 × все сценарии 19.1 в 1440×900 светлой +
//     канонический normal во всех окнах/темах.
func combosFor(stage string) []combo {
	if stage == "W3" {
		var cs []combo
		for _, wc := range windows {
			for _, theme := range wc.themes {
				for _, pg := range []string{"login", "queue"} {
					cs = append(cs, combo{scenario: "empty", page: pg, win: wc.name, theme: theme})
				}
			}
		}
		return cs
	}
	if stage == "W5" {
		// J1/J2 (19.5): карточка сессии на каждом шаге сценария, 1440 и 390.
		// Идём к именованной сессии сценария «normal», у которой уже нужное
		// состояние (в т.ч. PROMPT-панель у task-hold → секция запроса
		// разрешения). journey = имя файла journey-J<n>-<шаг>__<окно>.png.
		steps := []struct{ j, name string }{
			{"J1-1", "task-idle-1"}, // новая сессия (свободна) с композером
			{"J1-2", "task-q1"},     // «В очереди»
			{"J1-3", "task-run-1"},  // «Выполняется»
			{"J1-4", "task-idle-2"}, // результат: «Свободна», ход OK
			{"J2-1", "task-hold"},   // запрос разрешения (PROMPT-панель)
			{"J2-2", "task-idle-3"}, // ответ кнопкой: ход пошёл, «Свободна»
		}
		var cs []combo
		for _, s := range steps {
			for _, win := range []string{"1440x900", "390x844"} {
				cs = append(cs, combo{scenario: "normal", page: "session-card",
					win: win, theme: "light", sessName: s.name, journey: s.j})
			}
		}
		return cs
	}
	if stage == "W6" {
		// J6/J7/J8 (19.5): каждый шаг × 2 окна. page — базовая страница, к
		// которой ведёт navigateHash; дальнейшие действия — driveW6 (w6.go).
		// Уникальный scenario на КОМБО → свежий стенд (декомпозиция: шаги
		// само-достаточны, деструктивные J6-3/4, J7-3, J8-* не накапливают
		// состояние между 1440 и 390). Неизвестный scenario → seed «normal».
		var cs []combo
		for _, s := range w6Journey {
			for _, win := range []string{"1440x900", "390x844"} {
				cs = append(cs, combo{scenario: "w6-" + s.j + "-" + win, page: s.pg,
					win: win, theme: "light", journey: s.j})
			}
		}
		return cs
	}
	if stage == "W7" {
		return w7Combos()
	}
	if stage == "W8" {
		// Терминал в браузере (13.4): открытый оверлей xterm × 2 окна
		// (десктоп 1440×900, телефон 390×844). Эхо-терминал стенда.
		var cs []combo
		for _, win := range []string{"1440x900", "390x844"} {
			cs = append(cs, combo{scenario: "normal", page: "sessions",
				win: win, theme: "light", journey: "W8-T"})
		}
		return cs
	}
	var cs []combo
	// Ядро: все сценарии × все страницы в 1440×900 светлой (19.1 × 11).
	for _, sc := range w4Scenarios {
		for _, pg := range w4Pages {
			cs = append(cs, combo{scenario: sc, page: pg, win: "1440x900", theme: "light"})
		}
	}
	// Адаптивность/тема: канонический normal во всех окнах/темах.
	for _, wc := range windows {
		for _, theme := range wc.themes {
			if wc.name == "1440x900" && theme == "light" {
				continue // уже в ядре
			}
			for _, pg := range w4Pages {
				cs = append(cs, combo{scenario: "normal", page: pg, win: wc.name, theme: theme})
			}
		}
	}
	return cs
}

// W6 (19.5, J6/J7/J8): шаги пользовательских сценария на 1440×900 и 390×844.
// Все шаги — на сценарии «normal» (2 сервера srv-01/srv-02, узел node-01,
// базовая ревизия настроек). Состояние стенда накапливается в порядке шагов:
// J6 создаёт/удаляет серверы, J7 — узел, J8 — меняет/откатывает настройку
// (см. driveW6 в w6.go). journey — имя файла journey-J<n>-<шаг>__<окно>.png.
var w6Journey = []struct {
	j  string
	pg string
}{
	{"J6-1", "servers"}, // мастер сервера (диалог)
	{"J6-2", "servers"}, // пробный запрос (health ✓)
	{"J6-3", "servers"}, // «Удаляется» (удаление занятого srv-01)
	{"J6-4", "servers"}, // исчез (удаление srv-02)
	{"J7-1", "nodes"},   // подключение узла (команда установки)
	{"J7-2", "nodes"},   // узел онлайн
	{"J7-3", "nodes"},   // узел удалён (исчез)
	{"J8-1", "settings"}, // изменение настройки (сохранено)
	{"J8-2", "settings"}, // просмотр изменений (ревизии)
	{"J8-3", "settings"}, // откат ревизии (значение вернулось)
}

// W7 (Устойчивость, 19.5): J-rows × 2 окна. База (sc) — сценарий стенда, из
// которого доводят состояние шагом (driveW7, w7.go); суффикс «~J-<окно>» даёт
// свежий стенд на комбо (окна не делят деструктивное состояние). pg — базовая
// страница; sess — именованная сессия для карточки (session-card).
var w7Journey = []struct {
	j    string
	sc   string // базовый сценарий стенда
	pg   string
	sess string
}{
	{"J3", "faults", "servers", ""},        // сервер DOWN → «Недоступен», ход на другом
	{"J4", "faults", "servers", ""},        // OOM → «Карантин» → «Снять ходы»
	{"J5", "emergency", "queue", ""},       // аварийная остановка → «Вернуть все»
	{"J9", "w7-ext", "queue", ""},          // cron qwen -p → «Завершить»; runpilot exec → JOB
	{"J10", "attention", "session-card", "task-hold-agent"}, // кодер KILL → «Перезапустить»
	{"J11", "offline", "queue", ""},        // потеря связи → восстановление
	{"J12", "normal", "queue", ""},         // «Очистить очередь» → «Отменить»
}

// w7Combos — набор снимков W7: реакции UI на состояния раздела 7 (пер-строчка)
// + J-rows. 1440×900 светлая (содержимое не зависит от темы; тёмная — W3).
func w7Combos() []combo {
	var cs []combo
	// Пер-строчка: реакция интерфейса (1440 светлая). w7-* — алиасы стенда:
	// тот же сид/режим, но уникальное имя снимка (не перетирает golden W4).
	for _, c := range []struct{ sc, pg string }{
		{"w7-ext", "queue"},    // X-rows, S10: внешние кодеры + JOB в работе
		{"w7-faults", "servers"}, // S13/J3/J4: DOWN «Недоступен», QUARANTINED «Карантин»
		{"w7-emerg", "queue"},    // O8/S-/J5: красный баннер, всё «Требуют внимания»
		{"w7-safe", "queue"},     // K5: баннер безопасного режима
		{"w7-offline", "queue"},  // O3/J11: «Нет связи»
	} {
		cs = append(cs, combo{scenario: c.sc, page: c.pg, win: "1440x900", theme: "light"})
	}
	// Пер-строчка с навигацией (driveW7): здоровье узла (N9/N10), untested (C6/U3).
	cs = append(cs, combo{scenario: "w7-node", page: "nodes", win: "1440x900", theme: "light", journey: "W7-N"})
	cs = append(cs, combo{scenario: "w7-untested", page: "session-card", win: "1440x900", theme: "light", sessName: "task-untested", journey: "W7-U"})
	// J-rows (19.5): каждый шаг × 2 окна (свежий стенд на комбо).
	for _, j := range w7Journey {
		for _, win := range []string{"1440x900", "390x844"} {
			cs = append(cs, combo{scenario: j.sc + "~" + j.j + "-" + win, page: j.pg,
				win: win, theme: "light", journey: j.j, sessName: j.sess})
		}
	}
	return cs
}

// gStage — текущий этап (для выбора драйвера сценария в capturePage).
var gStage string

// driveJourney — выбор драйвера сценария по этапу: W7 → driveW7 (w7.go),
// иначе → driveW6 (w6.go).
func driveJourney(page *rod.Page, journey string) {
	if gStage == "W8" {
		driveW8(page, journey)
		return
	}
	if gStage == "W7" {
		driveW7(page, journey)
		return
	}
	driveW6(page, journey)
}

type fileChecks struct {
	Console  string   `json:"console"`
	Network  string   `json:"network"`
	Scroll   string   `json:"scroll"`
	Text     string   `json:"text"`
	Axe      string   `json:"axe"`
	Touch    string   `json:"touch"`
	Fonts    string   `json:"fonts"`
	Golden   string   `json:"golden"`
	Findings []string `json:"findings,omitempty"`
}

type manifest struct {
	Stage  string                `json:"stage"`
	Commit string                `json:"commit"`
	Time   string                `json:"time"`
	WebURL string                `json:"web_url,omitempty"`
	Files  []string              `json:"files"`
	Checks map[string]fileChecks `json:"checks"`
}

var (
	fStage     = flag.String("stage", "W3", "этап для out/ и manifest")
	fOut       = flag.String("out", "", "каталог скриншотов (по умолчанию docs/evidence/<stage>/screens)")
	fGolden    = flag.String("golden", "test/visual/golden", "каталог golden-базы")
	fStand     = flag.String("stand", "", "путь к бинарнику runpilot-stand")
	fToken     = flag.String("token", "", "операторский токен для входа")
	fStart     = flag.String("start", "2026-09-26T10:00:00Z", "старт виртуальных часов (RFC3339)")
	fChrome    = flag.String("chrome", "/usr/bin/google-chrome", "путь к Chrome/Chromium")
	fAxe       = flag.String("axe", "test/visual/axe.min.js", "путь к axe-core")
	fDiffMax   = flag.Float64("diff-max", 1.0, "максимальная разница golden в %")
	fSkipStand = flag.Bool("stand-running", false, "стенд уже запущен (не поднимать/не убивать)")
	fRunURL    = flag.String("run-url", "", "при --stand-running: готовый web_url")
	fScen      = flag.String("scenario", "", "только один сценарий (фильтр; пусто — все)")
)

const iphoneUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) " +
	"AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

// observerJS — ставится на КАЖДЫЙ новый документ (EvalOnNewDocument) ПЕРЕД
// скриптами приложения: собирает console.error/warn, ошибки и статусы fetch/XHR
// в window.__runpilot*. Никогда не модифицирует сеть.
const observerJS = `
window.__runpilotConsole = [];
window.__runpilotFetch = [];
(function(){
  ['error','warn'].forEach(function(lv){
    var o = console[lv];
    console[lv] = function(){
      try{ window.__runpilotConsole.push(lv+': '+[].map.call(arguments,function(a){try{return String(a);}catch(e){return '';}}).join(' ')); }catch(e){}
      return o.apply(this, arguments);
    };
  });
  window.addEventListener('error', function(e){
    try{ window.__runpilotConsole.push('error: '+(e.message||'')+(e.filename?(' @'+e.filename+':'+e.lineno):'')); }catch(err){}
  });
  window.addEventListener('unhandledrejection', function(e){
    try{ window.__runpilotConsole.push('rejection: '+String(e.reason)); }catch(err){}
  });
  var of = window.fetch;
  if (of) {
    window.fetch = function(){
      var u = (typeof arguments[0]==='string') ? arguments[0] : (arguments[0]&&arguments[0].url) || String(arguments[0]);
      var p = of.apply(this, arguments);
      p.then(function(res){ try{ window.__runpilotFetch.push({u:u, s:res.status}); }catch(e){} }).catch(function(){});
      return p;
    };
  }
  var oo = XMLHttpRequest.prototype.open, os = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function(m, u){ this.__u = u; return oo.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function(){
    this.addEventListener('loadend', function(){ try{ window.__runpilotFetch.push({u:this.__u, s:this.status}); }catch(e){} });
    return os.apply(this, arguments);
  };
})();
`

func main() {
	flag.Parse()
	gStage = *fStage
	if *fOut == "" {
		*fOut = "docs/evidence/" + *fStage + "/screens"
	}
	if *fStage == "" {
		fatalf("нужен --stage")
	}
	if *fToken == "" {
		fatalf("нужен --token")
	}
	if err := os.MkdirAll(*fOut, 0o755); err != nil {
		fatalf("out: %v", err)
	}
	commitB, _ := exec.Command("git", "rev-parse", "HEAD").Output()
	commit := strings.TrimSpace(string(commitB))

	// 2. Chrome (headless) + go-rod (один браузер на весь прогон).
	l := launcher.New().Bin(*fChrome).Headless(true).NoSandbox(true)
	l.Set(flags.Flag("disable-gpu"))
	l.Set(flags.Flag("disable-dev-shm-usage"))
	l.Set(flags.Flag("hide-scrollbars"))
	l.Set(flags.Flag("force-color-profile"), "srgb")
	l.Set(flags.Flag("no-first-run"))
	l.Set(flags.Flag("no-default-browser-check"))
	l.Set(flags.Flag("disable-extensions"))
	wsURL := l.MustLaunch()
	b := rod.New().ControlURL(wsURL).MustConnect()
	defer b.Close()

	axeSrc, err := os.ReadFile(*fAxe)
	if err != nil {
		fatalf("axe-core: %v", err)
	}

	combos := combosFor(*fStage)
	if *fScen != "" {
		filtered := []combo{}
		for _, c := range combos {
			if c.scenario == *fScen {
				filtered = append(filtered, c)
			}
		}
		combos = filtered
	}
	if len(combos) == 0 {
		fatalf("нет комбинаций для stage=%s scenario=%s", *fStage, *fScen)
	}

	// Группируем по сценарию (стенд на сценарий), сохраняя порядок.
	scOrder := []string{}
	bySc := map[string][]combo{}
	for _, c := range combos {
		if _, ok := bySc[c.scenario]; !ok {
			scOrder = append(scOrder, c.scenario)
		}
		bySc[c.scenario] = append(bySc[c.scenario], c)
	}

	m := manifest{
		Stage: *fStage, Commit: commit, Time: time.Now().UTC().Format(time.RFC3339),
		Checks: map[string]fileChecks{},
	}
	totalFindings := 0

	for _, sc := range scOrder {
		// 1. Стенд под сценарий.
		var webURL string
		var stand *exec.Cmd
		if !*fSkipStand {
			if *fStand == "" {
				fatalf("нужен --stand (бинарник runpilot-stand)")
			}
			tmp, err := os.MkdirTemp("", "runpilot-shot-*")
			if err != nil {
				fatalf("tmp: %v", err)
			}
			infoFile := filepath.Join(tmp, "stand.json")
			stand = exec.Command(*fStand, "-token", *fToken, "-start", *fStart,
				"-scenario", sc, "-info-file", infoFile)
			stand.Stdout = os.Stdout
			stand.Stderr = os.Stderr
			if err := stand.Start(); err != nil {
				fatalf("запуск стенда (%s): %v", sc, err)
			}
			webURL = waitForInfo(infoFile)
			fmt.Printf("screenshots: стенд %s: %s\n", sc, webURL)
		} else {
			if *fRunURL == "" {
				fatalf("--stand-running требует --run-url")
			}
			webURL = *fRunURL
		}
		m.WebURL = webURL

		// Один вход на стенд: cookie хранится в браузере и делится всеми
		// вкладками, поэтому повторно логиниться не нужно (иначе /web/login
		// упирается в rate-limit 429 на длинной серии снимков).
		loggedIn := false
		for _, c := range bySc[sc] {
			wc := windowByName(c.win)
			var name string
			if c.journey != "" {
				name = fmt.Sprintf("journey-%s__%s.png", c.journey, c.win)
			} else {
				name = fmt.Sprintf("%s__%s__%s__%s.png", sc, c.page, c.win, c.theme)
			}
			outPath := filepath.Join(*fOut, name)
			goldenPath := filepath.Join(*fGolden, name)
			fmt.Printf("  %s (%s/%s)\n", name, c.win, c.theme)

			chk, got, err := capturePage(b, webURL, wc, c.page, c.param, sc, c.theme, c.journey, c.sessName, *fToken, &loggedIn, string(axeSrc))
			if err != nil {
				chk.Findings = append(chk.Findings, "screenshot: "+err.Error())
			} else {
				if err := os.WriteFile(outPath, got, 0o644); err != nil {
					fatalf("запись %s: %v", name, err)
				}
				// Golden создаётся/сравнивается ТОЛЬКО при чистом рендере (0
				// находок 19.3): иначе в базу попадёт битый снимок, а повторный
				// прогон после фикса будет сравнивать с ним (ложный diff).
				if len(chk.Findings) != 0 {
					chk.Golden = "skipped (findings)"
				} else if _, err := os.Stat(goldenPath); os.IsNotExist(err) {
					if err := os.MkdirAll(*fGolden, 0o755); err == nil {
						_ = os.WriteFile(goldenPath, got, 0o644)
					}
					chk.Golden = "created"
				} else {
					base, _ := os.ReadFile(goldenPath)
					if d, derr := diffPct(base, got); derr != nil {
						chk.Findings = append(chk.Findings, "golden: "+derr.Error())
						chk.Golden = "error"
					} else if d > *fDiffMax {
						chk.Findings = append(chk.Findings,
							fmt.Sprintf("golden diff %.2f%% > %.2f%%", d, *fDiffMax))
						chk.Golden = fmt.Sprintf("diff %.2f%%", d)
					} else {
						chk.Golden = fmt.Sprintf("ok %.2f%%", d)
					}
				}
			}
			chk.Network = summary(chk.Findings, "network ")
			chk.Console = summary(chk.Findings, "console ")
			m.Checks[name] = chk
			m.Files = append(m.Files, name)
			totalFindings += len(chk.Findings)
		}

		if stand != nil {
			killStand(stand)
		}
	}

	// 4. Manifest.
	manPath := filepath.Join(*fOut, "manifest.json")
	if bb, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(manPath, bb, 0o644)
	}

	fmt.Printf("screenshots: %d файлов, %d находок\n", len(m.Files), totalFindings)
	for _, f := range m.Files {
		if len(m.Checks[f].Findings) > 0 {
			fmt.Printf("  [!] %s:\n", f)
			for _, x := range m.Checks[f].Findings {
				fmt.Printf("      - %s\n", x)
			}
		}
	}
	if totalFindings > 0 {
		fmt.Printf("FAIL: 19.3 — %d находок\n", totalFindings)
		os.Exit(1)
	}
	fmt.Println("OK: 19.3 — 0 находок")
}

// capturePage — одна вкладка: навигация + (сценарий) + проверки 19.3 + PNG.
// loggedIn — «стенд уже введён» (cookie в браузере, делится вкладками): вход
// выполняется один раз, дальше вкладки авторизованы cookie.
func capturePage(b *rod.Browser, webURL string, wc windowCfg, pg, param, scenario, theme, journey, sessName, token string, loggedIn *bool, axeSrc string) (fileChecks, []byte, error) {
	chk := fileChecks{}
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return chk, nil, err
	}
	defer page.Close()
	page.MustSetViewport(wc.w, wc.h, wc.scale, wc.phone)
	if wc.phone {
		page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: iphoneUA})
	}
	page.MustEvalOnNewDocument(observerJS)

	goCard := pg == "session-card"
	if pg == "login" {
		// Экран входа (W3): без авторизации.
		page.MustNavigate(webURL + "/web/login")
	} else if !*loggedIn {
		// Первый снимок на стенд: вход (cookie ставится в браузере).
		page.MustNavigate(webURL + "/web/login")
		doLogin(page, token)
		*loggedIn = true
		// После входа мы на "/" — переходим к странице (hash-роут).
		if goCard {
			page.MustNavigate(webURL + "/#/sessions")
			time.Sleep(1200 * time.Millisecond)
			if journey != "" {
				gotoCardByName(page, sessName)
			} else {
				gotoCard(page, param)
			}
		} else {
			navigateHash(page, pg)
		}
	} else {
		// Дальнейшие снимки: cookie уже в браузере — сразу на страницу.
		if goCard {
			page.MustNavigate(webURL + "/#/sessions")
			time.Sleep(1200 * time.Millisecond)
			if journey != "" {
				gotoCardByName(page, sessName)
			} else {
				gotoCard(page, param)
			}
		} else {
			page.MustNavigate(webURL + "/#/" + pg)
			time.Sleep(1600 * time.Millisecond) // догрузка данных (loopback)
		}
	}
	if !goCard && journey != "" {
		driveJourney(page, journey) // W6: J6/J7/J8, W7: J3/J4/J5/J9/J10/J11/J12
	}
	if goCard && journey != "" && wc.phone {
		scrollJourneyRight(page) // телефон: показать колонку состояния (why/разрешение/действия)
	}
	if pg != "login" && scenario == "offline" {
		dropSSE(page) // обрыв SSE → баннер «Нет связи» (19.1)
	}
	time.Sleep(300 * time.Millisecond) // стабилизация (без WaitStable: SSE)
	setTheme(page, theme)
	time.Sleep(400 * time.Millisecond) // отрисовка шрифтов/иконки

	// 19.3.3 — горизонтальный скролл.
	if chk.Scroll = chkEvalStr(page, `() => JSON.stringify({sw:document.documentElement.scrollWidth,cw:document.documentElement.clientWidth})`,
		func(s string) string {
			var sc struct{ SW, CW int }
			_ = json.Unmarshal([]byte(s), &sc)
			if sc.SW > sc.CW {
				return fmt.Sprintf("FAIL %d>%d", sc.SW, sc.CW)
			}
			return fmt.Sprintf("ok %d<=%d", sc.SW, sc.CW)
		}); chk.Scroll == "FAIL" {
		chk.Findings = append(chk.Findings, "scroll: "+strings.TrimPrefix(chk.Scroll, "FAIL "))
	} else if chk.Scroll == "" {
		chk.Scroll = "ok"
	}

	// 19.3.4 — мусор в видимом тексте.
	if txt, err := page.Eval("() => JSON.stringify(document.body.innerText)"); err == nil {
		var body string
		_ = json.Unmarshal([]byte(txt.Value.Str()), &body)
		found := false
		for _, bad := range []string{"undefined", "NaN", "null", "[object Object]", "Invalid Date", "{{"} {
			if strings.Contains(body, bad) {
				chk.Findings = append(chk.Findings, fmt.Sprintf("text: «%s» в тексте", bad))
				found = true
			}
		}
		chk.Text = "ok"
		if found {
			chk.Text = "FAIL"
		}
	}

	// 19.3.5 — axe-core: 0 serious/critical. Инжектим на финальном документе.
	srcJSON, _ := json.Marshal(axeSrc)
	page.MustEval(fmt.Sprintf("() => { window.__axeSrc = %s; }", srcJSON))
	page.MustEval("() => { (0,eval)(window.__axeSrc); }")
	if s, err := page.Eval(`
		() => (async () => {
			const r = await axe.run(document, {resultTypes:['violations']});
			return JSON.stringify(r.violations.map(function(v){
				return {impact:v.impact, id:v.id, nodes:v.nodes.map(function(n){
					return {t:(n.target||[]).join(','), f:(n.failureSummary||'').replace(/\n/g,' ').slice(0,140), h:(n.html||'').replace(/\s+/g,' ').slice(0,70)};
				})};
			}));
		})()`); err == nil {
		var viol []struct {
			Impact string
			ID     string
			Nodes  []struct {
				T, F, H string
			}
		}
		_ = json.Unmarshal([]byte(s.Value.Str()), &viol)
		n := 0
		for _, v := range viol {
			if v.Impact == "serious" || v.Impact == "critical" {
				n++
				node := ""
				if len(v.Nodes) > 0 {
					node = v.Nodes[0].H + " :: " + v.Nodes[0].F
					if len(node) > 140 {
						node = node[:140]
					}
				}
				chk.Findings = append(chk.Findings, fmt.Sprintf("axe: %s %s %s", v.Impact, v.ID, node))
			}
		}
		if n == 0 {
			chk.Axe = fmt.Sprintf("ok (0 serious/critical из %d)", len(viol))
		} else {
			chk.Axe = fmt.Sprintf("FAIL %d serious/critical", n)
		}
	} else {
		chk.Axe = "error"
	}

	// 19.3.6 — 44×44 (только телефон).
	if wc.phone {
		if s, err := page.Eval(`
			() => JSON.stringify((function(){
				var bad=[];
				document.querySelectorAll('a,button,input,select,textarea,[role="button"],[tabindex]').forEach(function(el){
					var r=el.getBoundingClientRect(); var cs=getComputedStyle(el);
					if(cs.display==='none'||cs.visibility==='hidden')return;
					if(r.width>0&&(r.width<44||r.height<44))
						bad.push(el.tagName+'.'+(typeof el.className==='string'?el.className:'')+' '+Math.round(r.width)+'x'+Math.round(r.height));
				});
				return bad;
			})())`); err == nil {
			var bad []string
			_ = json.Unmarshal([]byte(s.Value.Str()), &bad)
			for _, it := range bad {
				chk.Findings = append(chk.Findings, fmt.Sprintf("touch: <44x44 %s", it))
			}
			if len(bad) == 0 {
				chk.Touch = "ok"
			} else {
				chk.Touch = fmt.Sprintf("FAIL %d элементов", len(bad))
			}
		}
	} else {
		chk.Touch = "n/a"
	}

	// 19.3.7 — шрифты. Явно запрашиваем оба семейства (латиница+кириллица),
	// чтобы проверка подтверждала, что SHIPPED-файлы woff2 загрузились и
	// распались: document.fonts.check без load() возвращает false для
	// лениво-неиспользуемого шрифта (например mono на странице логина).
	if s, err := page.Eval(`
		() => (async () => {
			await document.fonts.load('16px Inter', 'AaБб');
			await document.fonts.load('16px "IBM Plex Mono"', 'AaБб');
			await document.fonts.ready;
			return JSON.stringify({inter:document.fonts.check('16px Inter','AaБб'),mono:document.fonts.check('16px "IBM Plex Mono"','AaБб')});
		})()`); err == nil {
		var f struct{ Inter, Mono bool }
		_ = json.Unmarshal([]byte(s.Value.Str()), &f)
		if f.Inter && f.Mono {
			chk.Fonts = "ok"
		} else {
			chk.Fonts = "FAIL"
			chk.Findings = append(chk.Findings, fmt.Sprintf("fonts: Inter=%v IBM Plex Mono=%v", f.Inter, f.Mono))
		}
	}

	// 19.3.1 — консоль (0 error/warn).
	if s, err := page.Eval("() => JSON.stringify(window.__runpilotConsole||[])"); err == nil {
		var c []string
		_ = json.Unmarshal([]byte(s.Value.Str()), &c)
		for _, f := range c {
			chk.Findings = append(chk.Findings, "console "+f)
		}
	}

	// 19.3.2 — сеть: 0 внешних хостов + 0 >=400.
	if s, err := page.Eval(`
		() => JSON.stringify({
			perf: performance.getEntriesByType('resource').map(function(e){
				return {n:e.name, re:e.responseEnd, ts:e.transferSize, eb:e.encodedBodySize, db:e.decodedBodySize};
			}),
			fetch: window.__runpilotFetch || []
		})`); err == nil {
		var net struct {
			Perf []struct {
				N              string
				RE, TS, EB, DB float64
			}
			Fetch []struct {
				U string
				S float64
			}
		}
		_ = json.Unmarshal([]byte(s.Value.Str()), &net)
		for _, f := range net.Fetch {
			if strings.Contains(f.U, "control/drop-sse") {
				continue
			}
			if host := hostOf(f.U); !isLocalHost(host) {
				chk.Findings = append(chk.Findings, "network внешний хост: "+f.U)
			}
			if f.S >= 400 {
				chk.Findings = append(chk.Findings, fmt.Sprintf("network HTTP %d: %s", int(f.S), f.U))
			}
		}
		for _, p := range net.Perf {
			if strings.HasPrefix(p.N, "data:") || strings.HasPrefix(p.N, "blob:") {
				continue
			}
			if host := hostOf(p.N); !isLocalHost(host) {
				chk.Findings = append(chk.Findings, "network внешний хост: "+p.N)
			}
			// Подзапрос, который не вернул данных — вероятный 404 (напр. favicon).
			if p.RE == 0 && p.TS == 0 && p.EB == 0 && p.DB == 0 && !strings.HasPrefix(p.N, "ws") {
				chk.Findings = append(chk.Findings, "network не загрузилось: "+p.N)
			}
		}
	}

	shot, err := page.Screenshot(true, &proto.PageCaptureScreenshot{Format: "png"})
	return chk, shot, err
}

// navigateHash — переход к странице (hash-роут) + догрузка её данных.
// Фиксированная задержка вместо WaitStable: открытая SSE-соединение держит
// страницу «неустойчивой» и WaitStable ждёт таймаут (медленно). На loopback
// данные страницы загружаются <100 мс, 1600 мс — с запасом.
func navigateHash(page *rod.Page, pg string) {
	page.MustEval(fmt.Sprintf("() => { location.hash = '%s'; }", "#/"+pg))
	time.Sleep(1600 * time.Millisecond) // догрузка данных страницы (loopback)
}

// gotoCardByName — пользовательский сценарий J1/J2 (19.5): перейти к карточке
// именованной сессии (у неё уже нужное состояние сценария, в т.ч. PROMPT-панель
// → секция запроса разрешения). Ищем по Name, переходим на /sessions/<sid>.
func gotoCardByName(page *rod.Page, name string) {
	page.MustEval(fmt.Sprintf(`async () => {
		const r = await fetch('/api/v1/state'); const d = await r.json();
		const ss = d.sessions || [];
		const s = ss.find(x => x.Name === %q) || ss[0];
		if (s) location.hash = '/sessions/' + s.SID;
	}`, name))
	// Переподключение общего SSE, если он закрыт (headless-артефакт перехода).
	_, _ = page.Eval(`() => { const es = window.__runpilotSSE ? window.__runpilotSSE() : null; if (es && es.readyState === 2 && window.__runpilotReconnectSSE) window.__runpilotReconnectSSE(); return 'ok'; }`)
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(`() => JSON.stringify({ card: !!document.querySelector('.sc-body'), offline: document.body.innerText.includes('Нет связи') })`)
		if err == nil {
			var st struct {
				Card    bool
				Offline bool
			}
			if json.Unmarshal([]byte(v.Value.Str()), &st) == nil && st.Card && !st.Offline {
				time.Sleep(500 * time.Millisecond) // стабилизация кадра
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(1800 * time.Millisecond) // запас, если условие не собралось
}

// scrollJourneyRight — телефон (390×844): в одном вьюпорте не поместиться ни
// шапка с бейджем, ни нижняя колонка состояния. Для шагов J1/J2 прокручиваем
// колонку состояния (.sc-right: сведения/почему/запрос разрешения/действия)
// вверх — она и есть смысловое ядро каждого шага.
func scrollJourneyRight(page *rod.Page) {
	page.MustEval(`() => { const el = document.querySelector('.sc-right'); if (el) el.scrollIntoView({block:'start'}); return 'ok'; }`)
	time.Sleep(450 * time.Millisecond) // прокрутка + стабилизация
}

// gotoCard — карточка сессии (W5): выбрать сессию по состоянию (session.State
// или состояние панели) и перейти на /sessions/<sid>. В headless-Chrome общий
// SSE-поток иногда остаётся закрытым (readyState=2) после перехода, хотя новый
// EventSource поднимается — если так, переподключаем его и ждём устойчивого
// онлайн (карточка отрисована и нет баннера «Нет связи»).
func gotoCard(page *rod.Page, state string) {
	page.MustEval(fmt.Sprintf(`async () => {
		const r = await fetch('/api/v1/state'); const d = await r.json();
		const pane = {}; for (const p of (d.panes || [])) { const pn = p.Pane || {}; if (pn.sid) pane[pn.sid] = pn.state; }
		const ss = d.sessions || [];
		const s = ss.find(x => (pane[x.SID] === '%s') || (x.State === '%s')) || ss[0];
		if (s) location.hash = '/sessions/' + s.SID;
	}`, state, state))
	// Переподключение общего SSE, если он закрыт (headless-артефакт перехода).
	_, _ = page.Eval(`() => { const es = window.__runpilotSSE ? window.__runpilotSSE() : null; if (es && es.readyState === 2 && window.__runpilotReconnectSSE) window.__runpilotReconnectSSE(); return 'ok'; }`)
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(`() => JSON.stringify({ card: !!document.querySelector('.sc-body'), offline: document.body.innerText.includes('Нет связи') })`)
		if err == nil {
			var st struct {
				Card    bool
				Offline bool
			}
			if json.Unmarshal([]byte(v.Value.Str()), &st) == nil && st.Card && !st.Offline {
				time.Sleep(500 * time.Millisecond) // стабилизация кадра
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(1800 * time.Millisecond) // запас, если условие не собралось
}

// dropSSE — обрыв SSE (сценарий offline): баннер «Нет связи».
func dropSSE(page *rod.Page) {
	page.MustEval("() => fetch('/control/drop-sse', {method:'POST'})")
	time.Sleep(700 * time.Millisecond)
}

// chkEvalStr — Eval JSON-строки и преобразовать её в однострочный статус.
func chkEvalStr(page *rod.Page, js string, fn func(string) string) string {
	v, err := page.Eval(js)
	if err != nil {
		return "error"
	}
	return fn(v.Value.Str())
}

func doLogin(page *rod.Page, token string) {
	time.Sleep(300 * time.Millisecond) // форма логина готова (MustNavigate ждал load)
	page.MustElement("#token").MustInput(token)
	page.MustElement("button[type=submit]").MustClick()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if v, err := page.Eval("() => location.pathname"); err == nil {
			if v.Value.Str() == "/" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func setTheme(page *rod.Page, theme string) {
	page.MustEval(fmt.Sprintf("() => { document.documentElement.dataset.theme = %q; }", theme))
}

func hostOf(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return strings.ToLower(u)
}

func isLocalHost(h string) bool {
	if h == "" {
		return true
	}
	if strings.HasPrefix(h, "127.") || strings.HasPrefix(h, "localhost") ||
		h == "0.0.0.0" || strings.HasPrefix(h, "[::1") || h == "::1" {
		return true
	}
	return false
}

func summary(findings []string, prefix string) string {
	for _, f := range findings {
		if strings.HasPrefix(f, prefix) {
			return "FAIL"
		}
	}
	return "ok"
}

func diffPct(a, b []byte) (float64, error) {
	ia, err := png.Decode(bytes.NewReader(a))
	if err != nil {
		return 0, fmt.Errorf("png a: %v", err)
	}
	ib, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return 0, fmt.Errorf("png b: %v", err)
	}
	ab, bb := ia.Bounds(), ib.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return 0, fmt.Errorf("размер: %dx%d != %dx%d", ab.Dx(), ab.Dy(), bb.Dx(), bb.Dy())
	}
	total := ab.Dx() * ab.Dy()
	differ := 0
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ra, ga, ba, _ := ia.At(x, y).RGBA()
			rb, gb, bb2, _ := ib.At(x, y).RGBA()
			if pixDiff(ra, rb) > 16 || pixDiff(ga, gb) > 16 || pixDiff(ba, bb2) > 16 {
				differ++
			}
		}
	}
	return float64(differ) / float64(total) * 100, nil
}

func pixDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func waitForInfo(path string) string {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if bb, err := os.ReadFile(path); err == nil {
			var info struct {
				WebURL string `json:"web_url"`
			}
			if json.Unmarshal(bb, &info) == nil && info.WebURL != "" {
				return info.WebURL
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	fatalf("стенд не записал info-file за 60с")
	return ""
}

func killStand(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "screenshots: "+format+"\n", args...)
	os.Exit(2)
}
