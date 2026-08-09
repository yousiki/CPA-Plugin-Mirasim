package ui

import "github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"

func LoginPage(state string) string {
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
<!-- data-cfasync="false": see the note on the CSP header in management/response.go. -->
<script data-cfasync="false">
(function () {
  var state = ` + jsString(state) + `;
  var base = ` + jsString(routes.ResourcePrefix) + `;
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
    call(` + jsString(routes.LoginCode) + `, { email: value }, send, "", function (body) {
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
    call(` + jsString(routes.LoginVerify) + `, { code: value }, verify, "", function (body) {
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
