// Moves every sidecar-managed Mirasim account onto the plugin, without re-login.
//
// The sidecar holds each account's refresh token; the plugin only needs that. Refresh
// tokens rotate but the previous one stays valid, so minting a fresh pair here does not
// disturb the still-running sidecar — the two can overlap safely during the cutover.
//
// Credentials are uploaded through the management API rather than written into the
// pgstore mirror directory: Postgres is authoritative and Bootstrap syncs the database
// over that directory on every start, so a file written there would vanish on restart.
import { Database } from "bun:sqlite";

const BASE = process.env.CPA_BASE_URL!;
const KEY = process.env.CPA_MANAGEMENT_KEY!;
const LOGIN = process.env.MIROFISH_LOGIN_URL!;
const DRY = process.env.DRY_RUN === "1";
const REFRESH_INTERVAL_SECONDS = 1500;

interface Row {
  email: string;
  refresh_token: string;
  suspended: number;
  needs_relogin: number;
}

const jwtExp = (token: string): number => {
  const payload = token.split(".")[1];
  if (!payload) return 0;
  try {
    return JSON.parse(Buffer.from(payload, "base64url").toString("utf8")).exp ?? 0;
  } catch {
    return 0;
  }
};

const db = new Database("/data/state.sqlite", { readonly: true });
const rows = db
  .query("SELECT email, refresh_token, suspended, needs_relogin FROM accounts ORDER BY email")
  .all() as Row[];

console.log(`${rows.length} accounts in the sidecar${DRY ? " (dry run)" : ""}`);
let ok = 0;
const failures: string[] = [];

for (const row of rows) {
  if (row.needs_relogin) {
    failures.push(`${row.email}: sidecar marked needs_relogin, skipped`);
    continue;
  }

  const res = await fetch(`${LOGIN}/auth/refresh`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ refresh_token: row.refresh_token }),
  });
  if (!res.ok) {
    failures.push(`${row.email}: /auth/refresh ${res.status} ${(await res.text()).slice(0, 120)}`);
    continue;
  }
  const body = (await res.json()) as { access_token?: string; token?: string; refresh_token?: string };
  const access = (body.access_token ?? body.token ?? "").trim();
  const refresh = (body.refresh_token ?? row.refresh_token).trim();
  if (!access) {
    failures.push(`${row.email}: /auth/refresh returned no access_token`);
    continue;
  }

  const exp = jwtExp(access);
  const doc: Record<string, unknown> = {
    type: "mirasim",
    email: row.email,
    access_token: access,
    refresh_token: refresh,
    last_refresh: new Date().toISOString().replace(/\.\d+Z$/, "Z"),
    refresh_interval_seconds: REFRESH_INTERVAL_SECONDS,
  };
  if (exp > 0) doc.expired = new Date(exp * 1000).toISOString().replace(/\.\d+Z$/, "Z");
  if (row.suspended) {
    doc.suspended = true;
    doc.disabled = true;
  }

  const name = `mirasim-${row.email}.json`;
  if (DRY) {
    console.log(`  would upload ${name} (exp ${doc.expired ?? "?"}${row.suspended ? ", suspended" : ""})`);
    ok += 1;
    continue;
  }

  const form = new FormData();
  form.append("file", new Blob([JSON.stringify(doc, null, 2)], { type: "application/json" }), name);
  const up = await fetch(`${BASE}/v0/management/auth-files`, {
    method: "POST",
    headers: { authorization: `Bearer ${KEY}` },
    body: form,
  });
  const upText = (await up.text()).slice(0, 200);
  if (!up.ok) {
    failures.push(`${row.email}: upload ${up.status} ${upText}`);
    continue;
  }
  console.log(`  uploaded ${name}`);
  ok += 1;
}

console.log(`\n${ok}/${rows.length} migrated`);
if (failures.length) {
  console.log("failures:");
  for (const failure of failures) console.log("  " + failure);
  process.exit(1);
}
