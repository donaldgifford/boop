---
id: INV-0003
title: "Kata VMM choice for sandboxed runs: Cloud Hypervisor or Firecracker"
status: Open
author: Donald Gifford
created: 2026-10-25
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0003: Kata VMM choice for sandboxed runs: Cloud Hypervisor or Firecracker

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

**Not started.** Opened on 2026-10-25 as a reminder from DESIGN-0005
OQ1 (option c), to come back to after the sandboxed tier runs on Kata's
default VMM.

Should boopd's sandboxed runs use Kata with Cloud Hypervisor or
Firecracker instead of the default QEMU, for faster VM boot and lower
per-pod overhead, and what would each need on the nodes (Firecracker's
devmapper snapshotter, the Talos extension's VMM support)?

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

**Triggered by:** DESIGN-0005 OQ1 (decided **a**: Kata through the
existing `kata` RuntimeClass; **c**, an explicit VMM choice, deferred to
this investigation).

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

- DESIGN-0005 § The runtime: Kata, and OQ1

<!--docz:references:end-->
