# mirasim — a CLIProxyAPI plugin

Makes Mirasim (Mirofish) a first-class CLIProxyAPI subscription, alongside Claude Code
and Codex: one login card in the management panel, email + verification code instead of
an OAuth callback, and credentials that CPA rotates on its own schedule.

It replaces `mirasim-sidecar`, a separate service that kept a `claude-api-key` credential
alive from the outside because CPA deliberately excludes API-key credentials from its
auto-refresh scheduler.

## What it does

| Capability | Purpose |
| --- | --- |
| `auth_provider` | Owns the login flow, parses stored credentials, refreshes the 1-hour access JWT. |
| `executor` | Forwards Anthropic Messages traffic (streaming included) to the relay. |
| `model_provider` | Advertises the verified model catalogue per logged-in account. |
| `management_api` | Serves the login page and the operator console. |

## How login works

The panel needs no changes. It reads `supports_oauth` / `oauth_provider` from
`GET /v0/management/plugins` and renders a login card for any plugin auth provider.

1. Clicking **Login** calls `GET /v0/management/mirasim-auth-url`, which reaches
   `auth.login.start`. The plugin mints a state and returns a link to its own page.
2. The page — `/v0/resource/plugins/mirasim/login?state=…` — asks for the account email,
   sends the code, then asks for the code.
3. The panel polls `GET /v0/management/get-auth-status?state=…`. Once the code has been
   verified, `auth.login.poll` hands the credential back and CPA persists it.
4. The login tab closes itself. The panel opened it with `window.open`, so it is allowed
   to; a tab opened by hand from the copied link shows a "you can close this tab"
   message instead.

The resource prefix is served without management authentication and **only for GET**, so
every step passes its arguments in the query string. That is the shape upstream's own
`host-callback-auth-files` example uses. The unguessable, 10-minute state is what
protects the login routes; the console has its own token.

The panel also renders an unused "callback URL" box for plugin providers. It is
unconditional in the panel and cannot be hidden from here — ignore it.

## Configuration

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    mirasim:
      enabled: true
      priority: 1
      public_base_url: "https://api.example.com"
      console_token: "<a long random string>"
```

| Key | Default | Notes |
| --- | --- | --- |
| `login_url` | `https://admin.test.mirofish.ai` | Mirofish auth backend; note it is the **staging** host. |
| `relay_url` | `https://mirasim-relay.mirofish.ai` | Anthropic-compatible gateway. |
| `public_base_url` | — | **Required in practice.** The host hands `auth.login.start` a `127.0.0.1` URL, which a remote browser cannot open. |
| `model_ids` | built-in list | `id[:contextLength],…` overrides the catalogue. |
| `console_token` | — | Without it the console returns 403. There is no other guard on that route. |
| `quota_probe` | `true` | Allows the console to read the relay's rate-limit headers. |
| `refresh_interval_seconds` | `1500` | Access tokens live ~3600s. |
| `context_beta` | `context-1m-2025-08-07` | The 1M context window is opted into with a header, never a model-name suffix. |
| `http_timeout_seconds` | `120` | Non-streaming upstream calls. |

## Console and quota

`/v0/resource/plugins/mirasim/status` lists accounts with their token expiry, 5h and 7d
usage windows, and accrued spend. Suspend and resume take an account out of and back into
rotation while keeping its refresh token. The page also shows up in the panel's plugin
menu, which embeds it in an iframe.

The shell is served without a token — it holds no account data, and the panel's iframe
has no way to supply one, so gating it would only ever render a raw 403 body. It asks for
`console_token` once, keeps it in the browser's local storage, and sends it on the data
and action calls, which are the ones that actually check. A rejected token is discarded
rather than retried, and with no `console_token` configured at all the page says so
instead of failing.

Quota is read by sending a deliberately invalid request (`max_tokens: 0`): the gateway
answers 400 with `x-litellm-response-cost: 0` yet still returns the rate-limit headers,
so the reading is free of model cost. It does spend one slot of the ~8000-request 5h
window, which is why probes are rationed:

- **automatically**, once per account per credential refresh (~25 min by default) — the
  probe rides `auth.refresh`, which is the same cadence the sidecar's 30-minute
  scheduler had, and needs no timer of its own;
- **once on demand**, when an account has no reading yet and the console is opened;
- **on request**, via the console's *Read quota* button, rate-limited to once a minute
  per account.

Everything else reads the cache, so leaving the console open costs nothing. Set
`quota_probe: false` to stop probing entirely.

Readings normalise utilization to a percentage (the gateway reports 0..1 for some
windows and 0..100 for others) and a `-1` means the header was absent. A 401 or 403 is
reported as a rejected credential rather than as an empty meter, so an unentitled
account cannot look like an idle one.

### In the management panel

The panel's Quota page can show Mirasim too, but only with the patch in `panel/`: its
providers are compiled in, so a plugin provider cannot appear there otherwise. With that
panel installed, a Mirasim tab joins the other five and each card reads
`GET /v0/management/mirasim/quota?auth_index=…` — a management-authenticated route that
serves the same cache, so the panel needs no console token.

Only windows the gateway actually reports are drawn. In practice that means the 7-day
window: the relay has never been observed to send the 5-hour headers, and an absent
window is omitted rather than drawn as an empty meter.

## Model catalogue

Only ids verified end to end through CPA with a claude-code-shaped request are
advertised. The relay publishes 40; the rest fail for this tenant (the `anthropic/*`
family answers 503, the `gpt-5.6*` family 403, the undated `claude-haiku-4-5` fails
claude-code's real payload with 400). See `defaultModels` in `config.go`.

No alias is set on any entry, on purpose: CPA advertises the alias to clients *instead
of* the name, which would hide the gateway ids.

## Two things worth knowing before you change the code

**Refresh runs through the executor.** The host looks the refresh handler up on the
executor bound to `auth.Provider` (`internal/pluginhost/adapters_executors.go`), so a
plugin auth provider without its own executor never gets `auth.refresh` called at all.
That is why this plugin ships one instead of reusing the built-in Claude executor.

**Suspension cannot use the host's `disabled` key.** The host rewrites that key from the
runtime record on every save, and the reload path after a plugin write does not read it
back, so a `disabled` written from here is reverted within milliseconds. The
provider-owned `suspended` field in the auth file survives the round trip, and
`auth.parse` turns it back into a genuinely disabled record.

## Build

The library is loaded into the CPA process, so the toolchain and libc must match the
server image (debian bookworm, glibc) exactly.

```bash
make build      # dist/mirasim.so for linux/amd64, via docker
make release    # + dist/mirasim_<version>_linux_amd64.zip and checksums.txt
make check      # vet, and confirm config.go and registry.json agree on the version
```

## Install

Publish a GitHub release whose tag is the version, with the two assets `make release`
produces, then point CPA at this repository's `registry.json`:

```yaml
plugins:
  store-sources:
    - "https://raw.githubusercontent.com/yousiki/CPA-mirasim-plugin/main/registry.json"
```

The plugin then appears in the panel's Plugin Store and installs to
`plugins/linux/amd64/mirasim-v<version>.so`. A private repository works too, via
`plugins.store-auth` with `type: github-token` and `token-env`.

To install by hand instead, drop `mirasim.so` into `<plugins-dir>/linux/amd64/`.

## Local development stack

`testing/` holds a throwaway stack: no tunnel, no watchtower, no postgres, loopback
port only, and plain files for config and credentials. It also mounts the patched panel
from `panel/` — run `panel/build.sh` before bringing it up, or the Quota page will have
no Mirasim tab.

```bash
# on the server, next to the production compose file
docker compose -f docker-compose.local.yaml -p mirasim-local up -d
ssh -L 18317:127.0.0.1:18317 <host>     # then browse http://127.0.0.1:18317/management.html
```

Management key `localtest`, client key `sk-local-mirasim-test`, console token
`localconsole`.

To tell the relay apart from a sibling gateway: it is Bedrock-backed and answers with an
`id` beginning `msg_bdrk_`, while sibling gateways answer `msg_01…`.

```bash
curl -H 'Authorization: Bearer sk-local-mirasim-test' -H 'content-type: application/json' \
  -d '{"model":"claude-opus-4-8","max_tokens":32,"messages":[{"role":"user","content":"ok"}]}' \
  http://127.0.0.1:18317/v1/messages
```
