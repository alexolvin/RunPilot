# W15 Этап 2 — медиа-пробы (P-image / P-many-image / P-video / P-many-video)

Пробы через шлюз (сессия с пином на сервер, неявная аренда). Числа в кадре — случайные (ffmpeg drawtext, DejaVu).

## vzu5 (2026-10-11T14:48:16Z)

- **P-image: PASS** — HTTP 200, найдено 275114
- **P-many-image: FAIL** — HTTP 400, HTTP 400
  - vLLM ответ: `{"error":{"message":"At most 8 image(s) may be provided in one prompt. (parameter=image)","type":"BadRequestError","param":"image","code":400}}`
- **P-video: PASS** — HTTP 200, найдены 65,85,85
- **P-many-video: PASS** — HTTP 200, HTTP 200

## 3p14 (2026-10-11T14:49:24Z)

- **P-image: FAIL** — HTTP 200, число 569163 не найдено
  - vLLM ответ: `{"id":"chatcmpl-92b5e32662e2f54d","object":"chat.completion","created":1791730164,"model":"qwen3.8-27b","choices":[{"index":0,"message":{"role":"assistant","content":"\n\n50103","refusal":null,"annotations":null,"audio":null,"function_call":null,"reasoning":"Пользователь спрашивает, какое число на картинке. На изображении чётко видны чёрные цифры на белом фоне: 5, 0, 1, 0, 3. То есть число 50103. `
- **P-many-image: FAIL** — HTTP 400, HTTP 400
  - vLLM ответ: `{"error":{"message":"At most 8 image(s) may be provided in one prompt. (parameter=image)","type":"BadRequestError","param":"image","code":400}}`
- **P-video: PASS** — HTTP 200, найдены 13,50,81
- **P-many-video: PASS** — HTTP 200, HTTP 200

## 3p14 P-image (повтор, проверка стабильности)

- P-image повтор: FAIL — HTTP 200, число 170949 не найдено
  - модель прочитала `tant","content":"\n\nНа картинке число **170049**.","refusal":null,"annotations":null,"audio":null,"` (ошибка чтения 6-значного числа)

## Итог по модальностям

| сервер | image | video | установленные input_modalities |
|---|---|---|---|
| vzu5 | не установлен (P-many-image: 400, лимит 8<20) | установлен (обе PASS) | [video] |
| 3p14 | не установлен (P-many-image: 400, лимит 8<20) | установлен (обе PASS) | [video] |

Оба сервера: vLLM `--limit-mm-per-prompt {"image":8,"video":4}` → P-many-image (N=20) всегда 400 → image не устанавливается. Video PASS на обоих → [video] установлен.

P-image 3p14: FAIL из-за ошибки модели (прочитала 170049 вместо 170949, 50103 вместо 569163) — HTTP 200, не вLLM. Не влияет на решение (image заблокирован P-many-image).