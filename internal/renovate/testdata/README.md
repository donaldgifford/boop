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

## The forced floor (IMPL-0006 task 1.1)

`renovate-44-forced-floor.log` is the pinned Renovate 44.149.2 in
`dryRun: extract` over `donaldgifford/boop-fixture-python-sdist`, with
every floor manager forced off the way an ordinary run in `enforce`
mode is: `force` in `RENOVATE_CONFIG`, each manager
`{enabled: false, managerFilePatterns: <its default>}`. Recorded on
2026-11-09 with:

```sh
# The forced object, from internal/renovate/unsafe (the same data
# jobspec's forceDisable uses).
cat > dev/forcegen/main.go <<'GO'
package main

import (
	"encoding/json"
	"fmt"

	"github.com/donaldgifford/boop/internal/renovate/unsafe"
)

func main() {
	force := map[string]any{}
	for _, m := range unsafe.Floor() {
		p, _ := unsafe.DefaultFilePatterns(m)
		if p == nil {
			p = []string{}
		}
		force[m] = map[string]any{"enabled": false, "managerFilePatterns": p}
	}
	b, _ := json.Marshal(force)
	fmt.Println(string(b))
}
GO
go run ./dev/forcegen > dev/force.json

IMG=$(sed -n 's/^    image: \(ghcr.io\/renovatebot\/renovate:\S*\)$/\1/p' charts/boopd/values.yaml)
CFG=$(jq -c '{dependencyDashboard:false, branchPrefix:"renovate-fixture/", force:.}' dev/force.json)
docker run --rm \
  -e RENOVATE_PLATFORM=github -e RENOVATE_TOKEN="$(gh auth token)" \
  -e RENOVATE_REPOSITORIES=donaldgifford/boop-fixture-python-sdist \
  -e RENOVATE_ONBOARDING=false -e RENOVATE_REQUIRE_CONFIG=required \
  -e RENOVATE_DRY_RUN=extract -e RENOVATE_REPORT_TYPE=logging \
  -e RENOVATE_CONFIG="$CFG" \
  -e LOG_FORMAT=json -e LOG_LEVEL=info \
  "$IMG" > dev/renovate-44-forced-floor.log
jq -c '.hostname="renovate"' dev/renovate-44-forced-floor.log \
  > internal/renovate/testdata/renovate-44-forced-floor.log
```

The report keeps `pip_requirements` and `renovate-config` and drops
`pip-compile`, which the repository's own config enables, with no
problems: the default patterns in the forced object (`[]` for
pip-compile) are what keeps Renovate from failing the repository.
`TestScanner_Renovate44ForcedFloor` pins it.
