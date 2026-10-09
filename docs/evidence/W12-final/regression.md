# W12 Stage 6: Regression

## go test -race

```
$ go test -race ./...
ok      runpilot/internal/command       (cached)
ok      runpilot/internal/config        (cached)
ok      runpilot/internal/detect        (cached)
ok      runpilot/internal/doctor        (cached)
ok      runpilot/internal/gateway       (cached)
ok      runpilot/internal/launchcmd     (cached)
?       runpilot/internal/logx  [no test files]
ok      runpilot/internal/model (cached)
ok      runpilot/internal/monitor       3.175s
ok      runpilot/internal/node  (cached)
?       runpilot/internal/node/pty      [no test files]
ok      runpilot/internal/notify        (cached)
ok      runpilot/internal/proto (cached)
ok      runpilot/internal/scheduler     (cached)
ok      runpilot/internal/sid   (cached)
ok      runpilot/internal/store (cached)
ok      runpilot/internal/testutil/fakellm      (cached)
ok      runpilot/internal/texts (cached)
?       runpilot/internal/tmux  [no test files]
ok      runpilot/internal/web   (cached)
```

## staticcheck

```
$ make staticcheck
CGO_ENABLED=0 go run honnef.co/go/tools/cmd/staticcheck@latest ./...
# (no output — clean)
```

## make lint

```
$ make lint
go run ./tools/lintlits
OK: R2 — запрещённых числовых/URL/IP/хост-литералов нет
go run ./tools/lintweb
OK: R2 — цвета/кириллица/числа в web/ на своих местах
go run ./tools/linthosts
OK: в коде нет имён реальных хостов и CGNAT-адресов машин
```

## e2e Э4

Не выполнен: требует 2×fakellm + 6×mockcoder, что вне рамок текущего стенда
(1×fakellm, 1 слот). Критерии: ≤1 concurrent lease/server, 0 timeouts,
p95 dispatch ≤ budget, exactly 18 turns, 0 phantoms.
