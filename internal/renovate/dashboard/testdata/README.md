# Dependency dashboard fixtures

`proxmox-34-*.md` are revisions of the Dependency Dashboard on
`donaldgifford/proxmox-go-sdk#34`, read from the issue's edit history
on 2026-11-21:

```sh
gh api graphql -f query='query{repository(owner:"donaldgifford",
  name:"proxmox-go-sdk"){issue(number:34){userContentEdits(first:50)
  {nodes{editedAt editor{login} diff}}}}}'
```

Each node's `diff` is the body after that edit.

| File | Edit | What it shows |
| --- | --- | --- |
| `proxmox-34-awaiting-schedule.md` | 2026-10-16T17:06:12Z, boop-bot | every branch under Awaiting Schedule |
| `proxmox-34-errored.md` | 2026-10-17T13:07:45Z, boop-bot | before the pin fix: `renovate/pin-dependencies` under Errored, and a repository problem |
| `proxmox-34-ticked.md` | 2026-10-17T15:02:50Z, donaldgifford | a human ticks retry on the pin branch |
| `proxmox-34-open.md` | 2026-10-17T15:05:16Z, boop-bot | after the fix: the pin branch is Open as PR #46 |
| `proxmox-34-rate-limited.md` | 2026-11-20T13:10:13Z, boop-bot | Rate-Limited items and the create-all box |

`every-section.md` is hand-built, with one item under each section
Renovate writes and every marker. `unknown-marker.md` has a marker this
package does not know, a ticked global box and the manual job box.
