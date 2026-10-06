# testdata/safety

Fixtures for the safety regression suite (invariants I1–I7).

Most invariant tests build their fixtures programmatically under `t.TempDir()`
so they exercise real filesystem behavior (identity swaps, symlink roots,
unwritable trash directories). The static files here are shared samples:

- `journal.jsonl` — a hand-written operation journal matching the spec §12
  schema. One run with an `owner` item and a `trash` item, framed by
  `run_start`/`run_end`.
- `whitelist` — a sample whitelist demonstrating comments and prefix matching.
- `protected.txt` — the default protected-path set, for reference.

The invariant suite lives in `internal/safe/safety_test.go`.