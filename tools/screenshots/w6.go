// Dрайвер пользовательских сценариев W6 (19.5, J6/J7/J8) для снимков: ведёт
// реальное UI (диалоги/формы) через DOM до нужного состояния перед снимком.
//
// Каждый КОМБО получает свежий стенд (уникальный scenario в combosFor), поэтому
// шаги само-достаточны: от состояния «normal» (2 сервера, узел node-01, базовая
// ревизия настроек) доводят систему до состояния шага. Клик — по видимому тексту
// кнопки с повтором; fill — через нативный value-setter + input/change-событие
// (контролируемые инпуты Preact). Числа/задержки — в tools/ (вне web/, R2 не
// применяется к задержкам драйвера).
package main

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
)

const (
	w6ClickTimeout = 4 * time.Second
	w6StepDelay    = 700 * time.Millisecond
	w6FieldDelay   = 250 * time.Millisecond
	w6NavDelay     = 1700 * time.Millisecond
	w6NetDelay     = 1600 * time.Millisecond
)

// driveW6 — довести UI к состоянию шага (см. w6Journey в main.go).
func driveW6(page *rod.Page, j string) {
	switch j {
	case "J6-1": // мастер сервера (диалог)
		w6Click(page, "Добавить сервер")
		w6WaitSel(page, ".dialog")
	case "J6-2": // пробный запрос с URL (пустой URL теперь = «не отвечает»);
		// /web/login отвечает 200 — служебный health стенда
		w6Click(page, "Добавить сервер")
		w6WaitSel(page, ".dialog")
		w6FillByLabel(page, "Имя*", "'j6-probe'")
		w6FillByLabel(page, "URL здоровья (health)", "location.origin + '/web/login'")
		w6Click(page, "Сервер не отвечает")
		w6WaitSel(page, ".dialog-ok")
	case "J6-3": // «Удаляется»: удаление занятого srv-01 (after_turns)
		w6Nav(page, "/servers/srv-01")
		w6Click(page, "Удалить сервер")
		w6WaitSel(page, ".dialog")
		w6Click(page, "Удалить")
		w6WaitGone(page, ".dialog")
		w6Resync(page)
		w6WaitText(page, "Удаляется")
	case "J6-4": // исчез: удаление idle srv-02 (now)
		w6Nav(page, "/servers/srv-02")
		w6Click(page, "Удалить сервер")
		w6WaitSel(page, ".dialog")
		w6Click(page, "Удалить")
		w6WaitGone(page, ".dialog")
		w6Resync(page)
		w6WaitCount(page, ".server-card", "1")
	case "J7-1": // подключение узла (команда установки)
		w6Click(page, "Подключить узел")
		w6WaitSel(page, ".code-block")
	case "J7-2": // узел онлайн (список node-01)
		w6WaitSel(page, ".node-card")
	case "J7-3": // узел удалён (исчез)
		w6Nav(page, "/nodes/node-01")
		w6Click(page, "Удалить узел")
		w6WaitSel(page, ".dialog")
		w6Click(page, "Удалить")
		w6WaitGone(page, ".dialog")
		w6Resync(page)
		w6WaitCount(page, ".node-card", "0")
	case "J8-1": // изменение настройки (сохранено)
		w6WaitSel(page, ".settings-search")
		w6FillInput(page, ".settings-search", "'requests_days'")
		w6BumpIntByPath(page, "retention.requests_days")
		w6Click(page, "Сохранить")
		w6Sleep(page, w6NetDelay)
	case "J8-2": // просмотр изменений (ревизии)
		w6WaitSel(page, ".settings-search")
		w6FillInput(page, ".settings-search", "'requests_days'")
		w6BumpIntByPath(page, "retention.requests_days")
		w6Click(page, "Сохранить")
		w6Sleep(page, w6NetDelay)
		w6Click(page, "Ревизии")
		w6WaitSel(page, ".rev-table")
	case "J8-3": // откат ревизии (значение вернулось)
		w6WaitSel(page, ".settings-search")
		w6FillInput(page, ".settings-search", "'requests_days'")
		w6BumpIntByPath(page, "retention.requests_days")
		w6Click(page, "Сохранить")
		w6Sleep(page, w6NetDelay)
		w6Click(page, "Ревизии")
		w6WaitSel(page, ".rev-table")
		w6RevertBaseline(page)
		w6Sleep(page, w6NetDelay)
	}
}

// w6Click — кликнуть первый видимый элемент (button/a/[role=button]) c точным
// текстом text; повтор до появления (UI рендерится асинхронно).
func w6Click(page *rod.Page, text string) {
	deadline := time.Now().Add(w6ClickTimeout)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`(() => {
			var els = document.querySelectorAll('button, a, [role="button"]');
			for (var i = 0; i < els.length; i++) {
				var e = els[i];
				if (e.textContent.trim() === %q && e.offsetParent !== null) { e.click(); return 'ok'; }
			}
			return 'miss';
		})()`, text))
		if err == nil && v.Value.String() == "ok" {
			time.Sleep(w6StepDelay)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(w6StepDelay)
}

// w6FillByLabel — заполнить input в диалоге по точному тексту <label>.
// jsValue — JS-выражение значения (строка/выражение), вставляется без кавычек.
func w6FillByLabel(page *rod.Page, label, jsValue string) {
	deadline := time.Now().Add(w6ClickTimeout)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`(() => {
			var labels = document.querySelectorAll('label');
			for (var i = 0; i < labels.length; i++) {
				if (labels[i].textContent.trim() === %q) {
					var f = labels[i].closest('.field');
					var el = f ? f.querySelector('input') : null;
					if (!el) return 'noinput';
					var setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
					setter.call(el, %s);
					el.dispatchEvent(new Event('input', {bubbles: true}));
					el.dispatchEvent(new Event('change', {bubbles: true}));
					return 'ok';
				}
			}
			return 'miss';
		})()`, label, jsValue))
		if err == nil && v.Value.String() == "ok" {
			time.Sleep(w6FieldDelay)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(w6FieldDelay)
}

// w6FillInput — заполнить input по CSS-селектору (JS-выражение значения).
func w6FillInput(page *rod.Page, sel, jsValue string) {
	page.Eval(fmt.Sprintf(`(() => {
		var el = document.querySelector(%q);
		if (!el) return 'miss';
		var proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
		var setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
		setter.call(el, %s);
		el.dispatchEvent(new Event('input', {bubbles: true}));
		el.dispatchEvent(new Event('change', {bubbles: true}));
		return 'ok';
	})()`, sel, jsValue), nil)
	time.Sleep(w6FieldDelay)
}

// w6BumpIntByPath — найти поле настроек по пути и увеличить int-значение на 1
// (гарантированно валидное изменение → dirty → сохранение создаст ревизию).
func w6BumpIntByPath(page *rod.Page, path string) {
	page.Eval(fmt.Sprintf(`(() => {
		var fields = document.querySelectorAll('.settings-group .field');
		for (var i = 0; i < fields.length; i++) {
			var err = fields[i].querySelector('.field-err');
			if (err && err.textContent.indexOf(%q) === 0) {
				var el = fields[i].querySelector('input');
				if (!el) return 'noinput';
				var cur = parseInt(el.value, 10);
				if (isNaN(cur)) cur = 0;
				var setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
				setter.call(el, String(cur + 1));
				el.dispatchEvent(new Event('input', {bubbles: true}));
				el.dispatchEvent(new Event('change', {bubbles: true}));
				return 'ok';
			}
		}
		return 'miss';
	})()`, path), nil)
	time.Sleep(w6FieldDelay)
}

// w6RevertBaseline — откатить базовую ревизию (source=migration): значение
// вернётся к исходному, появится новая запись (source=revert).
func w6RevertBaseline(page *rod.Page) {
	page.Eval(`(() => {
		var rows = document.querySelectorAll('.rev-table tbody tr');
		for (var i = 0; i < rows.length; i++) {
			if (rows[i].textContent.indexOf('migration') !== -1) {
				var btn = rows[i].querySelector('button');
				if (btn) { btn.click(); return 'ok'; }
			}
		}
		return 'miss';
	})()`, nil)
	time.Sleep(w6StepDelay)
}

// w6Nav — переход к hash-маршруту (детальная карточка) + догрузка.
func w6Nav(page *rod.Page, path string) {
	page.Eval(fmt.Sprintf(`() => { location.hash = %q; }`, "#"+path), nil)
	time.Sleep(w6NavDelay)
}

// w6WaitSel — ждать появления селектора (пока есть, до 8 с).
func w6WaitSel(page *rod.Page, sel string) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`() => JSON.stringify(!!document.querySelector(%q))`, sel))
		if err == nil && v.Value.String() == "true" {
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
}

// w6Resync — полный срез /api/v1/state (тестовый хук __runpilotResync = RESYNC).
// После деструктивных действий (удаление) состав списков меняется; точечные
// SSE-события его не обновляют — нужен свежий срез (как RESYNC в проде).
func w6Resync(page *rod.Page) {
	page.Eval(`() => { try { window.__runpilotResync(); } catch (e) {} }`, nil)
}

// w6WaitText — ждать появления текста в теле страницы (до 8 с).
func w6WaitText(page *rod.Page, text string) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`() => document.body.innerText.indexOf(%q) !== -1`, text))
		if err == nil && v.Value.String() == "true" {
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
}

// w6WaitCount — ждать, пока количество элементов sel равно n (n строкой).
func w6WaitCount(page *rod.Page, sel, n string) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`() => document.querySelectorAll(%q).length === %s`, sel, n))
		if err == nil && v.Value.String() == "true" {
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
}

// w6WaitGone — ждать исчезновения селектора (диалог закрыт → навигация прошла).
func w6WaitGone(page *rod.Page, sel string) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, err := page.Eval(fmt.Sprintf(`() => !document.querySelector(%q)`, sel))
		if err == nil && v.Value.String() == "true" {
			time.Sleep(300 * time.Millisecond)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// w6Sleep — пауза на асинхронную реакцию (сеть/рендер) перед снимком.
func w6Sleep(page *rod.Page, d time.Duration) { time.Sleep(d) }
