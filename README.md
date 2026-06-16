# cofiswarm-dispatch

Cofiswarm component: `dispatch`.

- Layout: [REPO-STANDARD-LAYOUT](https://github.com/keepdevops/cofiswarmdev/blob/main/docs/REPO-STANDARD-LAYOUT.md)
- Migration: [MIGRATION-SPRINTS](https://github.com/keepdevops/cofiswarmdev/blob/main/docs/MIGRATION-SPRINTS.md)

## FHS paths

| Path | Purpose |
|------|---------|
| `/etc/cofiswarm/dispatch/` | config |
| `/var/lib/cofiswarm/dispatch/` | state |
| `/var/log/cofiswarm/dispatch/` | logs |

## Test

```bash
./test/scripts/assert-layout.sh dispatch
```
