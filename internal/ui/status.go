package ui

import (
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"
)

// StatusPage renders the console shell.
//
// `configured` reports whether a console token exists at all, so the page can tell
// "locked, enter the token" apart from "the operator never set one" instead of leaving the
// reader staring at a permanent 403.
func StatusPage(configured bool, token string) string {
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
  <label for="tokenInput">This console is protected by <code>plugins.configs.` + config.PluginID + `.console_token</code>.</label>
  <input id="tokenInput" type="password" autocomplete="off" spellcheck="false" placeholder="console token">
  <button id="unlockBtn" type="button">Unlock</button>
</fieldset>

<div id="controls" class="hidden">
  <button id="reload" class="link" type="button">Reload</button>
  <button id="quota" class="link" type="button">Read quota</button>
  <button id="suspendAll" class="link" type="button">Suspend all</button>
  <button id="resumeAll" class="link" type="button">Resume all</button>
  <button id="forget" class="link" type="button">Forget token</button>
</div>
<table id="table" class="hidden"><thead><tr>
  <th>Account</th><th>State</th><th>Token</th><th>5h window</th><th>7d window</th><th></th>
</tr></thead><tbody id="rows"></tbody></table>
<div id="msg" class="msg hidden"></div>
<footer id="foot"></footer>
</main>
<!-- data-cfasync="false": see the note on the CSP header in management/response.go. -->
<script data-cfasync="false">
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
  var base = ` + jsString(routes.ResourcePrefix) + `;
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

      var quota = account.quota || null;

      var state = document.createElement("td");
      var pill = document.createElement("span");
      pill.className = "pill" + (account.disabled || account.unavailable ? " off" : "");
      pill.textContent = account.disabled ? "suspended" : (account.status || "active");
      // The probe's own complaint (a rejected credential, missing headers) has no column
      // of its own; hang it off the state, which is what an operator looks at first.
      if (quota && quota.error) { pill.title = quota.error; }
      state.appendChild(pill);
      tr.appendChild(state);

      var expiry = document.createElement("td");
      expiry.textContent = duration(account.seconds_left);
      tr.appendChild(expiry);

      tr.appendChild(quotaCell(quota && quota.utilization_5h, quota && quota.reset_5h, data.now));
      tr.appendChild(quotaCell(quota && quota.utilization_7d, quota && quota.reset_7d, data.now));

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
        fetch(base + ` + jsString(routes.StatusAction) + ` + "?" + query.toString())
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
    fetch(base + ` + jsString(routes.StatusData) + ` + "?" + query.toString())
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

  // -- bulk suspend / resume --------------------------------------------------
  //
  // Two-step arming rather than window.confirm(): this page is embedded in the
  // management panel's iframe, and a sandbox attribute without allow-modals makes
  // confirm() return false with no dialog — the button would look dead. Arming needs no
  // modal and no extra CSP allowance.
  var ARM_MS = 5000;
  var suspendAll = document.getElementById("suspendAll");
  var resumeAll = document.getElementById("resumeAll");

  function bulk(button, op, label) {
    var armed = false;
    var timer = 0;
    function disarm() {
      armed = false;
      window.clearTimeout(timer);
      button.textContent = label;
      button.classList.remove("armed");
    }
    button.addEventListener("click", function () {
      if (!armed) {
        armed = true;
        button.textContent = "Click again to confirm";
        button.classList.add("armed");
        timer = window.setTimeout(disarm, ARM_MS);
        return;
      }
      disarm();
      suspendAll.disabled = true;
      resumeAll.disabled = true;
      var query = new URLSearchParams({ token: token, op: op });
      fetch(base + ` + jsString(routes.StatusAction) + ` + "?" + query.toString())
        .then(function (r) { return r.json(); })
        .then(function (body) {
          suspendAll.disabled = false;
          resumeAll.disabled = false;
          if (body.ok !== true) { show(body.error || "action failed", false); return; }
          var failed = body.failed || [];
          var text = (op === "suspend_all" ? "Suspended " : "Resumed ") + (body.changed || 0) + " account(s)";
          if (body.skipped) { text += " · " + body.skipped + " already " + (op === "suspend_all" ? "suspended" : "active"); }
          if (failed.length) {
            text += " · " + failed.length + " failed: " + failed.map(function (f) {
              return (f.email || f.auth_index) + " (" + f.error + ")";
            }).join("; ");
          }
          show(text, failed.length === 0);
          load(false);
        })
        .catch(function (e) {
          suspendAll.disabled = false;
          resumeAll.disabled = false;
          show(String(e), false);
        });
    });
  }

  bulk(suspendAll, "suspend_all", "Suspend all");
  bulk(resumeAll, "resume_all", "Resume all");

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
    show("This console is disabled. Set plugins.configs.` + config.PluginID + `.console_token in the CPA config to enable it.", false);
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
