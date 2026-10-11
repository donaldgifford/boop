# Renovate log fixtures

The `synthetic-*.log` files are hand-built Renovate JSON log lines. They
follow the message table in DESIGN-0001 § Process builder and the
report shape `{problems, repositories: {"<slug>": {problems, branches,
packageFiles}}}`.

- `synthetic-live.log`: a live run with two branches, three PR events, a
  `Repository finished` line and a `Printing report` line.
- `synthetic-dryrun.log`: the same run with `dryRun: full`.
- `synthetic-truncated-report.log`: the live run with its report line
  cut in half.

They stand in for the real fixtures of IMPL-0001 task 4.5, which needs a
scratch repository and a token. That task records the upstream
Renovate 44 image's output against a scratch repository, once with
`dryRun: full` and once live, with `RENOVATE_REPORT_TYPE=logging`. It
commits the sanitized logs here as `renovate-44-live.log` and
`renovate-44-dryrun.log`, with the `docker run` command that produced
them. The golden tests then run over the real files too, so a change in
Renovate's message strings fails a test.
