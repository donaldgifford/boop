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

## Renovate 44

`renovate-44-dryrun.log` and `renovate-44-live.log` are real output of
Renovate 44.179.2 against a scratch repository (IMPL-0001 task 4.5),
recorded on 2026-10-23 with:

```sh
TOKEN="$(gh auth token)"; REPO=<owner>/<scratch>
for mode in full null; do
  docker run --rm \
    -e RENOVATE_PLATFORM=github -e RENOVATE_TOKEN="$TOKEN" \
    -e RENOVATE_REPOSITORIES="$REPO" \
    -e RENOVATE_ONBOARDING=false -e RENOVATE_REQUIRE_CONFIG=required \
    -e RENOVATE_DRY_RUN=$mode -e RENOVATE_REPORT_TYPE=logging \
    -e RENOVATE_FORCE='{"schedule":["at any time"],"dependencyDashboard":false,"branchPrefix":"renovate-fixture/"}' \
    -e LOG_FORMAT=json -e LOG_LEVEL=info \
    ghcr.io/renovatebot/renovate:44 > dev/renovate-44-$mode.log
done
```

`RENOVATE_FORCE` keeps the runs from colliding with boop-bot on a
repository it manages: no schedule gate, no second Dependency
Dashboard, and branches under `renovate-fixture/`. Close the live run's
PRs afterwards. `LOG_LEVEL=info` is what boopd's Jobs use.

Sanitized with:

```sh
jq -c '.hostname="renovate"' dev/renovate-44-<mode>.log \
  | sed 's#<owner>/<scratch>#boop-bot/scratch#g'
```

The live run created two PRs and aborted two branches ("Workflows
update rejection"), because the gh token had no `workflow` scope. Both
reports carry `packageFiles[].deps[]`. The golden tests
(`TestScanner_Renovate44*`) run over these files, so a change in
Renovate's message strings or report shape fails a test, not a run.
