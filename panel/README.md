# Management panel patch

Adds a Mirasim card to the panel's Quota page.

## Why a patch is needed

The panel cannot be extended at runtime. Its quota providers are compiled in:

- `QuotaProviderType` is a closed union of five strings
  (`src/features/quota/providers/types.ts`)
- `QUOTA_ADAPTERS` is a `Record` over that union, each entry binding a React body and a
  dedicated store slice (`src/features/quota/providers/index.ts`)
- every `filterFn` is an exact match against one provider string
  (`src/utils/quota/validators.ts`)

A credential whose provider is `mirasim` matches none of them, so it never reaches a
tab. Masquerading as `claude` does not help either: that adapter fetches Anthropic's own
usage endpoint, which the relay does not serve.

## What the patch adds

| File | Change |
| --- | --- |
| `providers/mirasim/data.ts` | Reads `GET /v0/management/mirasim/quota`, turns the reading into rows |
| `providers/mirasim/MirasimQuotaBody.tsx` | Window meters plus the accrued spend |
| `types/quota.ts`, `stores/useQuotaStore.ts`, `providers/types.ts` | State type and store slice |
| `providers/index.ts`, `logic.ts`, `constants.ts`, `resetSchedule.ts` | Registration, tab order, reset scheduling |
| `utils/quota/validators.ts` | `isMirasimFile` |
| `authFiles/constants.ts`, `assets/icons/mirasim.svg` | Tab icon and provider label |
| `authFiles/components/AuthFileQuotaSection.tsx` | The exhaustive switch needs the new arm |
| `i18n/locales/*.json` | `mirasim_quota.*` in all four locales |
| `tests/quotaPageLogic.test.ts` | Tab-count fixture gains a `mirasim: 0` entry |

Unlike the other five providers the data does not come from an upstream usage endpoint:
the relay only reports usage in Anthropic's unified rate-limit response headers, so
reading it costs a real request. The plugin does that server-side on its own schedule and
caches it; the panel just reads the cache.

## Build

```bash
./build.sh              # -> dist/management.html
./build.sh /tmp/mp.html # or an explicit path
```

The script pins upstream to a release tag, applies the patch with `--3way`, runs
`type-check` and the test suite, then builds. If the patch stops applying after an
upstream release, resolve the conflict in the work tree and re-export:

```bash
git -C <worktree> add -A src tests
git -C <worktree> diff --cached > 0001-mirasim-quota-adapter.patch
```

Then bump `UPSTREAM_REF` in `build.sh` to the release you rebased onto.

## Install

See the note printed by `build.sh`. Both halves matter: `MANAGEMENT_STATIC_PATH` selects
the file, and `remote-management.disable-auto-update-panel: true` stops CPA's asset
updater from overwriting it — the updater targets exactly that path and compares the
file's hash against the upstream release on every startup.

The panel is read per request, so replacing the file takes effect immediately; no restart
is needed.

## Verifying a deployment

`build.sh` stamps the patch's hash into the version string, so the served panel states
exactly which build it is:

```bash
curl -s <base>/management.html | grep -o 'v[0-9.]*+mirasim\.[0-9a-f]\{8\}'
```

Compare that with the build id `build.sh` printed.

Do **not** verify by grepping for `mirasim`, or by byte count against a remembered
number. Every build of this patch contains the word, so it cannot distinguish a current
build from a stale one — a stale artifact reached production exactly that way and
white-screened the quota page for everyone.
