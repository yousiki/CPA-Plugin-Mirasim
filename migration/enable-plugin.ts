// Enables the mirasim plugin and pins the management panel, through CPA's own config
// API. Editing the file in the pgstore mirror does not work: Bootstrap syncs the
// database over the local copy on every start, so the edit is silently reverted.
const BASE = process.env.CPA_BASE_URL!;
const KEY = process.env.CPA_MANAGEMENT_KEY!;
const headers = { authorization: `Bearer ${KEY}` };

const res = await fetch(`${BASE}/v0/management/config.yaml`, { headers });
if (!res.ok) throw new Error(`GET config.yaml failed: ${res.status}`);
let text = await res.text();
console.log("fetched", res.status, text.length, "bytes");

const pluginsAnchor = 'plugins:\n  enabled: false\n  dir: "plugins"';
if (!text.includes(pluginsAnchor)) throw new Error("plugins anchor missing");
text = text.replace(pluginsAnchor, 'plugins:\n  enabled: true\n  dir: "plugins"');

// The block already carries an empty `configs: {}` further down, past a run of
// commented-out store settings. Filling it in is the only correct edit; adding a second
// `configs:` key makes the whole document fail to unmarshal.
const configsAnchor = "\n  configs: {}\n";
const occurrences = text.split(configsAnchor).length - 1;
if (occurrences !== 1) throw new Error(`expected exactly one 'configs: {}', found ${occurrences}`);
text = text.replace(
  configsAnchor,
  [
    "",
    "  configs:",
    "    mirasim:",
    "      enabled: true",
    "      priority: 1",
    '      public_base_url: "https://api.siki.moe"',
    `      console_token: "${process.env.MIRASIM_CONSOLE_TOKEN}"`,
    "      quota_probe: true",
    "      refresh_interval_seconds: 1500",
    "",
  ].join("\n"),
);

// Only an uncommented key counts: the shipped config carries a commented example of
// this very setting, and matching that would skip the insert while reporting success.
if (!/^[ \t]*disable-auto-update-panel:/m.test(text)) {
  const anchor = "  allow-remote: true";
  if (!text.includes(anchor)) throw new Error("allow-remote anchor missing");
  text = text.replace(anchor, `${anchor}\n  disable-auto-update-panel: true`);
  console.log("panel auto-update disabled");
}

const put = await fetch(`${BASE}/v0/management/config.yaml`, {
  method: "PUT",
  headers: { ...headers, "content-type": "application/yaml" },
  body: text,
});
console.log("PUT", put.status, (await put.text()).slice(0, 200));
