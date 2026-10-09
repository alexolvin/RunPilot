# Stage 1: Проверка tmux (влияние на оператора)

Дата: 2026-10-09 ~15:18 MSK

## Причина

Stand node (PID 792546, `/home/user/.local/bin/runpilot node --config
/tmp/runpilot-stand/node.yaml`) сканировал дефолтный tmux-сокет оператора —
нарушение правила 13. Stand node остановлен (kill 792546).

## Состояние до очистки

Панели дефолтного сокета с `@runpilot_sid`:

| Панель | Сессия | @runpilot_sid | В рабочем DB | Действие |
|---|---|---|---|---|
| %20 | acc-a | QZAJM6PP3Y | да (IDLE) | оставить |
| %21 | acc-b | 85WECWD7BX | да (IDLE) | оставить |
| %22 | acc2-a | ASXQAM0Q11 | да (IDLE) | оставить |
| %23 | acc3-a | F75B1EW1XM | да (IDLE) | оставить |
| %18 | hold-b | 87PNW4PQ64 | да (IDLE) | оставить |
| %19 | hold-b2 | 7SM5NAT93H | да (IDLE) | оставить |
| %30 | held-a2 | WVTDDVWASP | **нет** | удалить |
| %31 | held-b2 | WX2SSY5R70 | **нет** | удалить |
| %32 | held-b3 | 7P2KC5VF3P | **нет** | удалить |
| %33 | held-b4 | 0TXDQPYNZF | **нет** | удалить |
| %34 | held-b5 | T11JWG9J54 | **нет** | удалить |

Stand DB (`/tmp/runpilot-stand/runpilot.db`): только e4-тестовые сессии (GONE),
регистраций панелей оператора нет (таблицы `pane` в stand DB нет).

## Очистка

```
tmux -L default set-option -pu -t %30 @runpilot_sid
tmux -L default set-option -pu -t %30 @runpilot_summary
# ... аналогично %31 %32 %33 %34
```

## Состояние после очистки

- Сиротские `@runpilot_sid`/`@runpilot_summary` на %30–%34 удалены.
- 6 живых сессий оператора (acc-a, acc-b, acc2-a, acc3-a, hold-b, hold-b2)
  не тронуты.
- Stand node остановлен; более не сканирует дефолтный сокет.
