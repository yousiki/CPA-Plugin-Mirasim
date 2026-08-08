# One-off migration scripts

Used once to move the production stack from `mirasim-sidecar` to this plugin. Kept
because they encode things that are not obvious from the CPA config, and that cost real
debugging to find.

All three read their target from the environment and hold no secrets:

```
CPA_BASE_URL=http://api:8317
CPA_MANAGEMENT_KEY=...
MIROFISH_LOGIN_URL=https://admin.test.mirofish.ai   # migrate only
MIRASIM_CONSOLE_TOKEN=...                           # enable only
```

They are Bun scripts. On the stack they ran inside the sidecar container, which already
had the database, the management key and network access; after the sidecar is gone, a
throwaway container on the same network works:

```bash
docker run --rm --network <stack>_default \
  -v "$PWD/migration/drop-sidecar-entries.ts:/tmp/s.ts" \
  -e CPA_BASE_URL=http://api:8317 -e CPA_MANAGEMENT_KEY=... \
  oven/bun:latest bun run /tmp/s.ts
```

`migrate-from-sidecar.ts` and `drop-sidecar-entries.ts` both honour `DRY_RUN=1`.

## What they exist to work around

**`enable-plugin.ts`** — writes config through `PUT /v0/management/config.yaml` instead
of editing `config.yaml`. With a Postgres store the database is authoritative and
`Bootstrap` syncs it over the local mirror at startup, so a file edit is reverted by the
very restart meant to apply it. It also fills the existing `configs: {}` key rather than
adding a second `configs:`, which would make the document fail to unmarshal.

**`migrate-from-sidecar.ts`** — exchanges each account's refresh token for a fresh pair
and uploads the credential through `POST /v0/management/auth-files`. Writing files into
the mirrored auth directory has the same problem as the config. Refresh tokens rotate but
the previous one stays valid, so this does not disturb a still-running sidecar.

**`drop-sidecar-entries.ts`** — replaces the `claude-api-key` list. `PUT` takes a bare
array, not the `{"claude-api-key": [...]}` shape `GET` returns, and replaces the whole
list; `auth-index` is server-computed and must not be sent back. Entries not pointing at
the relay are preserved.

## Order

Stop the sidecar **before** dropping its entries — it self-heals missing credentials and
would recreate them on its next cycle. Migrating the accounts first is safe and keeps the
overlap window covered by both systems.
