# go test -race + staticcheck

Дата: 2026-10-06.

## go test -race ./...

```
ok  runpilot/internal/api       13.375s
ok  runpilot/internal/backup     1.267s
ok  runpilot/internal/clock      1.011s
ok  runpilot/internal/command    1.027s
ok  runpilot/internal/config     1.040s
ok  runpilot/internal/detect     1.074s
ok  runpilot/internal/doctor     1.056s
ok  runpilot/internal/gateway    16.065s
ok  runpilot/internal/launchcmd  1.038s
ok  runpilot/internal/model      1.041s
ok  runpilot/internal/monitor    2.805s
ok  runpilot/internal/node       2.964s
ok  runpilot/internal/notify     1.037s
ok  runpilot/internal/proto      1.037s
ok  runpilot/internal/scheduler  13.546s
ok  runpilot/internal/sid        1.052s
ok  runpilot/internal/store      4.350s
ok  runpilot/internal/testutil/fakellm 1.547s
ok  runpilot/internal/texts      1.041s
ok  runpilot/internal/web        2.309s
```

**0 race-детектов, 0 fall.**

## staticcheck ./...

```
$ go run honnef.co/go/tools/cmd/staticcheck@latest ./...
(exit 0, нет замечаний)
```

**0 замечаний.** (Исходно были 2: SA4010 unused append в hold_rank_capacity_test.go,
U1000 unused idlePane в hold_virtual_test.go — устранены.)
