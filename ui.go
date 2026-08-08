package main

// Self-contained HTML for the two browser pages. No external assets, so the pages work
// behind a tunnel and under a restrictive CSP.

import "encoding/json"

const pageStyle = `
:root { color-scheme: light dark; }
* { box-sizing: border-box; }
body { margin: 0; padding: 2.5rem 1.25rem; font: 15px/1.55 ui-sans-serif, -apple-system, "Segoe UI", Roboto, sans-serif;
       background: Canvas; color: CanvasText; }
main { max-width: 46rem; margin: 0 auto; }
h1 { font-size: 1.35rem; margin: 0 0 .35rem; }
p.sub { margin: 0 0 1.75rem; opacity: .7; }
fieldset { border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: .6rem;
           padding: 1.1rem 1.15rem; margin: 0 0 1rem; }
legend { padding: 0 .4rem; font-weight: 600; font-size: .9rem; }
label { display: block; font-size: .82rem; opacity: .75; margin-bottom: .35rem; }
input { width: 100%; padding: .6rem .7rem; font: inherit; border-radius: .45rem;
        border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: Canvas; color: CanvasText; }
button { margin-top: .8rem; padding: .55rem 1.1rem; font: inherit; font-weight: 600; cursor: pointer;
         border-radius: .45rem; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent);
         background: color-mix(in srgb, CanvasText 8%, Canvas); color: CanvasText; }
button:disabled { opacity: .45; cursor: not-allowed; }
button.link { margin: 0; padding: .25rem .55rem; font-size: .8rem; font-weight: 500; }
.msg { margin-top: .9rem; padding: .6rem .75rem; border-radius: .45rem; font-size: .88rem; white-space: pre-wrap; }
.msg.ok { background: color-mix(in srgb, #16a34a 16%, transparent); }
.msg.err { background: color-mix(in srgb, #dc2626 16%, transparent); }
.hidden { display: none; }
table { width: 100%; border-collapse: collapse; font-size: .88rem; }
th, td { text-align: left; padding: .5rem .4rem; border-bottom: 1px solid color-mix(in srgb, CanvasText 12%, transparent); }
th { font-weight: 600; opacity: .7; font-size: .8rem; }
td.actions { text-align: right; white-space: nowrap; }
.pill { display: inline-block; padding: .1rem .5rem; border-radius: 999px; font-size: .76rem;
        background: color-mix(in srgb, CanvasText 10%, transparent); }
.pill.off { background: color-mix(in srgb, #dc2626 18%, transparent); }
.meter { width: 100%; min-width: 4.5rem; height: .38rem; border-radius: 999px; overflow: hidden;
         background: color-mix(in srgb, CanvasText 14%, transparent); margin-top: .25rem; }
.meter > span { display: block; height: 100%; border-radius: 999px; background: #16a34a; }
.meter > span.mid { background: #f59e0b; }
.meter > span.high { background: #dc2626; }
.dim { opacity: .55; }
footer { margin-top: 1.5rem; font-size: .8rem; opacity: .6; }
`

// jsString renders a Go string as a JSON literal safe to inline in a <script> block.
// encoding/json escapes <, > and & to \u00XX, so an embedded "</script>" cannot close
// the element early.
func jsString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func boolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func loginPageHTML(state string) string {
	return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in to Mirasim</title>
<style>` + pageStyle + `</style>
</head><body><main>
<h1>Sign in to Mirasim</h1>
<p class="sub">Enter the account email, then the 6-digit code it receives. Leave this tab open until the panel reports success.</p>

<fieldset>
  <legend>1 &middot; Email</legend>
  <label for="email">Account email</label>
  <input id="email" type="email" autocomplete="email" autocapitalize="off" spellcheck="false" placeholder="you@example.com">
  <button id="send" type="button">Send code</button>
</fieldset>

<fieldset id="step2" class="hidden">
  <legend>2 &middot; Verification code</legend>
  <label for="code">6-digit code</label>
  <input id="code" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="12" placeholder="123456">
  <button id="verify" type="button">Verify and finish</button>
</fieldset>

<div id="msg" class="msg hidden"></div>
<footer>This page talks only to this proxy. The verification code is single-use and the session expires in 10 minutes.</footer>
</main>
<script>
(function () {
  var state = ` + jsString(state) + `;
  var base = "/v0/resource/plugins/` + pluginID + `";
  var email = document.getElementById("email");
  var code = document.getElementById("code");
  var send = document.getElementById("send");
  var verify = document.getElementById("verify");
  var step2 = document.getElementById("step2");
  var msg = document.getElementById("msg");

  function show(text, ok) {
    msg.textContent = text;
    msg.className = "msg " + (ok ? "ok" : "err");
  }

  function call(path, params, button, label, done) {
    button.disabled = true;
    var query = new URLSearchParams(params);
    query.set("state", state);
    fetch(base + path + "?" + query.toString(), { headers: { accept: "application/json" } })
      .then(function (r) { return r.json().then(function (b) { return { status: r.status, body: b }; }); })
      .then(function (r) {
        button.disabled = false;
        if (!r.body || r.body.ok !== true) {
          show((r.body && r.body.error) || ("request failed with HTTP " + r.status), false);
          return;
        }
        done(r.body);
      })
      .catch(function (e) { button.disabled = false; show(String(e), false); });
  }

  send.addEventListener("click", function () {
    var value = (email.value || "").trim();
    if (!value) { show("Enter an email address first.", false); return; }
    call("/login/code", { email: value }, send, "", function (body) {
      step2.classList.remove("hidden");
      code.focus();
      show(body.dev_code
        ? ("Code sent to " + body.email + ". Staging echoed it back: " + body.dev_code)
        : ("Code sent to " + body.email + ". Check the inbox."), true);
    });
  });

  verify.addEventListener("click", function () {
    var value = (code.value || "").trim();
    if (!value) { show("Enter the code you received.", false); return; }
    call("/login/verify", { code: value }, verify, "", function (body) {
      verify.disabled = true;
      send.disabled = true;
      show("Signed in as " + body.email + ". Closing this tab…", true);
      // The panel opened this tab with window.open, so it is allowed to close itself.
      // Browsers that refuse (or a tab opened by hand from the copied link) fall through
      // to the message below; the panel finishes saving the credential either way.
      window.setTimeout(function () {
        window.close();
        window.setTimeout(function () {
          show("Signed in as " + body.email + ". The credential is saved — you can close this tab.", true);
        }, 400);
      }, 1200);
    });
  });

  if (!state) { show("This link is missing its login state. Start the login again from the panel.", false); }
})();
</script>
</body></html>`
}

// statusPageHTML renders the console shell.
//
// `configured` reports whether a console token exists at all, so the page can tell
// "locked, enter the token" apart from "the operator never set one" instead of leaving
// the reader staring at a permanent 403.
func statusPageHTML(configured bool, token string) string {
	return `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Mirasim accounts</title>
<style>` + pageStyle + `</style>
</head><body><main>
<h1>Mirasim accounts</h1>
<p class="sub" id="sub">Loading&hellip;</p>

<fieldset id="unlock" class="hidden">
  <legend>Console token</legend>
  <label for="tokenInput">This console is protected by <code>plugins.configs.mirasim.console_token</code>.</label>
  <input id="tokenInput" type="password" autocomplete="off" spellcheck="false" placeholder="console token">
  <button id="unlockBtn" type="button">Unlock</button>
</fieldset>

<div id="controls" class="hidden">
  <button id="reload" class="link" type="button">Reload</button>
  <button id="quota" class="link" type="button">Read quota</button>
  <button id="forget" class="link" type="button">Forget token</button>
</div>
<table id="table" class="hidden"><thead><tr>
  <th>Account</th><th>State</th><th>Token</th><th>5h window</th><th>7d window</th><th>Spend</th><th></th>
</tr></thead><tbody id="rows"></tbody></table>
<div id="msg" class="msg hidden"></div>
<footer id="foot"></footer>
</main>
<script>
(function () {
  var configured = ` + boolLiteral(configured) + `;
  var STORAGE_KEY = "mirasim.console_token";
  // Query parameter first (a copied link still works), then whatever was unlocked
  // earlier in this browser. The shell itself needs no token, so an empty value simply
  // shows the unlock form rather than an error.
  var token = ` + jsString(token) + `;
  if (!token) {
    try { token = window.localStorage.getItem(STORAGE_KEY) || ""; } catch (e) { token = ""; }
  }
  var base = "/v0/resource/plugins/` + pluginID + `";
  var rows = document.getElementById("rows");
  var msg = document.getElementById("msg");
  var sub = document.getElementById("sub");
  var foot = document.getElementById("foot");

  function show(text, ok) {
    msg.textContent = text;
    msg.className = "msg " + (ok ? "ok" : "err");
  }

  function duration(seconds) {
    if (seconds === null || seconds === undefined) { return "—"; }
    if (seconds <= 0) { return "expired"; }
    var m = Math.floor(seconds / 60);
    if (m < 60) { return m + "m"; }
    var h = Math.floor(m / 60);
    return h < 48 ? (h + "h " + (m % 60) + "m") : (Math.floor(h / 24) + "d " + (h % 24) + "h");
  }

  // One window's cell: a percentage, a meter, and when the window rolls over.
  function quotaCell(percent, resetAt, now) {
    var td = document.createElement("td");
    if (percent === null || percent === undefined || percent < 0) {
      td.textContent = "—";
      td.className = "dim";
      return td;
    }
    var label = document.createElement("div");
    label.textContent = percent.toFixed(percent < 10 ? 1 : 0) + "%";
    td.appendChild(label);

    var meter = document.createElement("div");
    meter.className = "meter";
    var fill = document.createElement("span");
    fill.style.width = Math.max(0, Math.min(100, percent)) + "%";
    if (percent >= 90) { fill.className = "high"; }
    else if (percent >= 60) { fill.className = "mid"; }
    meter.appendChild(fill);
    td.appendChild(meter);

    if (resetAt) {
      var reset = document.createElement("div");
      reset.className = "dim";
      reset.style.fontSize = ".76rem";
      reset.textContent = "resets in " + duration(resetAt - now);
      td.appendChild(reset);
    }
    return td;
  }

  function render(data) {
    rows.textContent = "";
    (data.accounts || []).forEach(function (account) {
      var tr = document.createElement("tr");

      var name = document.createElement("td");
      name.textContent = account.email || account.label || account.name;
      tr.appendChild(name);

      var state = document.createElement("td");
      var pill = document.createElement("span");
      pill.className = "pill" + (account.disabled || account.unavailable ? " off" : "");
      pill.textContent = account.disabled ? "suspended" : (account.status || "active");
      state.appendChild(pill);
      tr.appendChild(state);

      var expiry = document.createElement("td");
      expiry.textContent = duration(account.seconds_left);
      tr.appendChild(expiry);

      var quota = account.quota || null;
      tr.appendChild(quotaCell(quota && quota.utilization_5h, quota && quota.reset_5h, data.now));
      tr.appendChild(quotaCell(quota && quota.utilization_7d, quota && quota.reset_7d, data.now));

      var spend = document.createElement("td");
      if (quota && quota.key_spend >= 0) {
        // x-litellm-key-spend, reported without a unit — shown as the raw figure rather
        // than dressed up as a currency it may not be.
        spend.textContent = Number(quota.key_spend).toLocaleString(undefined, {
          maximumFractionDigits: 2
        });
      } else {
        spend.textContent = "—";
        spend.className = "dim";
      }
      if (quota && quota.error) {
        spend.title = quota.error;
      }
      tr.appendChild(spend);

      var actions = document.createElement("td");
      actions.className = "actions";
      var button = document.createElement("button");
      button.className = "link";
      button.type = "button";
      button.textContent = account.disabled ? "Resume" : "Suspend";
      button.addEventListener("click", function () {
        button.disabled = true;
        var query = new URLSearchParams({
          token: token,
          op: account.disabled ? "resume" : "suspend",
          auth_index: account.auth_index
        });
        fetch(base + "/status/action?" + query.toString())
          .then(function (r) { return r.json(); })
          .then(function (body) {
            if (body.ok !== true) { show(body.error || "action failed", false); button.disabled = false; return; }
            show((body.disabled ? "Suspended " : "Resumed ") + (account.email || account.name), true);
            load(false);
          })
          .catch(function (e) { show(String(e), false); button.disabled = false; });
      });
      actions.appendChild(button);
      tr.appendChild(actions);

      rows.appendChild(tr);
    });

    var readings = (data.accounts || []).filter(function (a) { return a.quota && a.quota.at; });
    var newest = readings.reduce(function (max, a) { return Math.max(max, a.quota.at); }, 0);
    sub.textContent = (data.accounts || []).length + " account(s) via " + data.relay_url
      + (newest ? " · quota read " + duration(data.now - newest) + " ago" : "")
      + (data.quota_enabled ? "" : " · quota probing disabled");

    foot.textContent = "Advertised models: " + (data.models || []).join(", ");
    if (!(data.accounts || []).length) {
      show("No Mirasim credentials yet. Add one from the panel's OAuth page.", true);
    }
  }

  var unlock = document.getElementById("unlock");
  var controls = document.getElementById("controls");
  var table = document.getElementById("table");
  var tokenInput = document.getElementById("tokenInput");

  function setLocked(locked, note) {
    unlock.classList.toggle("hidden", !locked);
    controls.classList.toggle("hidden", locked);
    table.classList.toggle("hidden", locked);
    if (locked) {
      rows.textContent = "";
      sub.textContent = configured ? "Locked" : "Console disabled";
      if (note) { show(note, false); }
      if (configured) { tokenInput.focus(); }
    }
  }

  function remember(value) {
    token = value;
    try { window.localStorage.setItem(STORAGE_KEY, value); } catch (e) { /* private mode */ }
  }

  function forget() {
    token = "";
    try { window.localStorage.removeItem(STORAGE_KEY); } catch (e) { /* private mode */ }
  }

  function load(withQuota) {
    if (!token) { setLocked(true); return; }
    var query = new URLSearchParams({ token: token, quota: withQuota ? "1" : "0" });
    fetch(base + "/status/data?" + query.toString())
      .then(function (r) { return r.json().then(function (b) { return { status: r.status, body: b }; }); })
      .then(function (r) {
        if (r.status === 403) {
          // A stale token is worse than none: it would fail silently on every reload.
          forget();
          setLocked(true, r.body.error || "invalid console token");
          return;
        }
        if (!r.body || r.body.ok !== true) {
          show((r.body && r.body.error) || "failed to load", false);
          return;
        }
        setLocked(false);
        msg.className = "msg hidden";
        render(r.body);
      })
      .catch(function (e) { show(String(e), false); });
  }

  document.getElementById("unlockBtn").addEventListener("click", function () {
    var value = (tokenInput.value || "").trim();
    if (!value) { show("Enter the console token.", false); return; }
    remember(value);
    tokenInput.value = "";
    msg.className = "msg hidden";
    load(false);
  });
  tokenInput.addEventListener("keydown", function (e) {
    if (e.key === "Enter") { document.getElementById("unlockBtn").click(); }
  });
  document.getElementById("forget").addEventListener("click", function () {
    forget();
    setLocked(true);
  });
  document.getElementById("reload").addEventListener("click", function () { load(false); });
  document.getElementById("quota").addEventListener("click", function () {
    show("Reading rate-limit headers… this spends one request slot per account.", true);
    load(true);
  });

  if (!configured) {
    setLocked(true);
    unlock.classList.add("hidden");
    show("This console is disabled. Set plugins.configs.` + pluginID + `.console_token in the CPA config to enable it.", false);
  } else {
    // Cached readings. The automatic probe rides the credential refresh cycle, so a page
    // load is free once an account has been read at least once; an account with no
    // reading yet is probed once to fill the cache.
    load(false);
  }
})();
</script>
</body></html>`
}
