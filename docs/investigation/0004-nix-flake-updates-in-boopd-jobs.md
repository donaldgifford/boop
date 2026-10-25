---
id: INV-0004
title: "Nix flake updates in boopd Jobs"
status: Open
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0004: Nix flake updates in boopd Jobs

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1](#observation-1)
  - [Observation 2](#observation-2)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

**Not started.** Opened on 2026-10-25 as a reminder, to come back to.

Can a boopd Job update a repository's `flake.lock` with Renovate's nix
manager, and what does it take: the preset, the Job's filesystem and
user, the nix install, the token, and a fixture that proves it?

Known so far, from 2026-10-25 (not yet tested):

- Renovate 44's nix manager ships `enabled: false`;
  `github>donaldgifford/renovate-config:nix` adds `packageRules` only
  and never enables it, so no repository's flake is updated today.
- The manager updates only flakes with a `flake.lock`
  (`nix flake update <inputs>`).
- boopd's Jobs run the chart's pinned `renovate:44-full` (44.149.2)
  with `binarySource=global`, so nothing is installed at run time.
  That image has `/usr/local/bin/nix` as a 721-byte containerbase shim
  and no `/nix`; the slim `renovate:44` has no `nix` at all. The Job
  runs as uid 12021 with a read-only root filesystem, and whether the
  shim's nix can create its store there is the first thing to test.
- Renovate passes the GitHub token on the command line
  (`--extra-access-tokens github.com=<token>`).

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

<!-- What do you expect to find, and why? This forces upfront thinking and
     makes the conclusion more meaningful. -->

<!--docz:hypothesis:end-->

<!--docz:context:start-->
## Context

<!-- Why is this investigation needed right now? What design, plan, or
     error triggered it? Link to the parent document(s). -->

**Triggered by:** the question whether
`github>donaldgifford/renovate-config:nix` runs in boopd (2026-10-25);
related to DESIGN-0005 (`nix flake update` locks inputs and is not on
its floor; the token on the command line bears on OQ4 and OQ5).

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

<!-- How will you test the hypothesis? List the specific steps, experiments,
     or code paths you will exercise. Keep it concrete enough that someone
     else could replicate the investigation. -->

1.
2.
3.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

<!-- Versions, configuration, or setup details relevant to reproducibility.
     Delete this section if not applicable. -->

| Component | Version / Value |
| --------- | --------------- |
|           |                 |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

<!-- What did you actually observe? Include command output, logs, benchmark
     numbers, or code snippets as evidence. Fill this in as you go. -->

### Observation 1

### Observation 2

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

<!-- Answer the original question directly. State clearly what was found:
     confirmed / refuted / inconclusive, and why. -->

**Answer:** <!-- Yes / No / Inconclusive -->

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

<!-- What should happen next based on this conclusion? Update the parent
     doc, unblock the design decision, open a follow-up investigation, etc. -->

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- DESIGN-0005 § What runs repository code, OQ4, OQ5
- `github>donaldgifford/renovate-config:nix`
- Renovate 44.179.2 `dist/modules/manager/nix` (`index.js`,
  `artifacts.js`)

<!--docz:references:end-->
