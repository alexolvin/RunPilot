# W12 Stage 5: Flaps test на синтетических событиях

## Тест

`TestFlaps` в `internal/monitor/flaps_test.go`

## Вывод

```
$ go test -v -run TestFlaps ./internal/monitor/
=== RUN   TestFlaps
--- PASS: TestFlaps (0.00s)
PASS
ok      runpilot/internal/monitor       0.003s
```

## Сценарий

1. Создаётся монитор с одним сервером `srv1`
2. Записываются 3 перехода UP↔DOWN: 5, 3, 1 минуту назад
3. `Flaps(10m)` → Count=3, LastFlap = 1 минуту назад
4. `Flaps(2m)` → Count=1 (только переход 1 минуту назад)
5. `Flaps(30s)` → 0 серверов (нет переходов в окне)
