# tuios daemon contract suite

Test-only. Runs mtm's real daemon client (`internal/daemon`) against throwaway
`tuios --server` daemons of several versions and reports what differs.

```sh
internal/daemon/contract/build-tuios.sh /tmp/mtmd-0-contract   # prints MTM_CONTRACT_TUIOS=a,b
MTM_CONTRACT_TUIOS=/tmp/mtmd-0-contract/bin/tuios-v0.10.0,/tmp/mtmd-0-contract/bin/tuios-v0.12.1 \
  GOFLAGS=-trimpath nice -n 20 go test -tags tuioscontract -count=1 -v ./internal/daemon/contract/
```

- `MTM_CONTRACT_REPORT=DIR` writes per-version results, registry and reply/event shapes as JSON.
- `MTM_CONTRACT_SEED=break` seeds a failure (an unknown parameter on every direct call, a wrong expected field); the suite must then fail.
- Each daemon runs under a temp root with scrubbed `HOME`, `XDG_*`, `TUIOS_*`; mtm's own `Ensure`/`StartServer` starts it (a `tuios` symlink first on PATH). It is stopped by the PID in its own pid file.
- `expectedDifferences` (harness_test.go) lists per-version known gaps. A listed check that starts passing fails the suite, so the list cannot go stale.
- Verbs whose call sites live in `cmd/mtm` (package main) are called with a mirrored parameter map; the file:line is in a comment.
