// Removes the sidecar's claude-api-key credentials now that the plugin serves Mirasim.
//
// PUT replaces the whole list and takes a bare array, not the {"claude-api-key": [...]}
// shape GET returns; auth-index is server-computed and must not be sent back. Entries
// that do not point at the relay are preserved verbatim.
const BASE = process.env.CPA_BASE_URL!;
const KEY = process.env.CPA_MANAGEMENT_KEY!;
const RELAY = process.env.MIRASIM_RELAY_URL ?? "https://mirasim-relay.mirofish.ai";
const DRY = process.env.DRY_RUN === "1";
const headers = { authorization: `Bearer ${KEY}` };

const body = await (await fetch(`${BASE}/v0/management/claude-api-key`, { headers })).json();
const entries: any[] = body["claude-api-key"] ?? [];
const isRelay = (e: any) => String(e["base-url"] ?? "").replace(/\/+$/, "") === RELAY.replace(/\/+$/, "");

const keep = entries.filter((e) => !isRelay(e)).map(({ "auth-index": _drop, ...rest }) => rest);
console.log(`${entries.length} entries, dropping ${entries.length - keep.length}, keeping ${keep.length}`);

if (DRY) {
  console.log("dry run, nothing written");
} else {
  const put = await fetch(`${BASE}/v0/management/claude-api-key`, {
    method: "PUT",
    headers: { ...headers, "content-type": "application/json" },
    body: JSON.stringify(keep),
  });
  console.log("PUT", put.status, (await put.text()).slice(0, 200));

  const after = await (await fetch(`${BASE}/v0/management/claude-api-key`, { headers })).json();
  const remaining: any[] = after["claude-api-key"] ?? [];
  console.log(`after: ${remaining.length} entries, ${remaining.filter(isRelay).length} still on the relay`);
}
