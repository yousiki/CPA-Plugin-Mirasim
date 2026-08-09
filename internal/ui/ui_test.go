package ui

import (
	"strings"
	"testing"
)

func TestStatusPageOffersBulkActions(t *testing.T) {
	page := StatusPage(true, "")

	for _, want := range []string{
		`id="suspendAll"`, `id="resumeAll"`,
		`"suspend_all"`, `"resume_all"`,
		"Click again to confirm",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("status page is missing %s", want)
		}
	}
	// confirm() is unusable here: the panel embeds this page in an iframe, and a sandbox
	// without allow-modals makes it return false with no dialog, so the button would look
	// dead. The two-step arming above replaces it. Matching on a call that passes a
	// message keeps the bare `confirm()` in the explanatory comments from tripping this.
	for _, call := range []string{`confirm("`, `confirm('`, "confirm(`"} {
		if strings.Contains(page, call) {
			t.Errorf("status page calls %s…, which a sandboxed iframe can silently refuse", call)
		}
	}
}

// The gateway only reports the shared virtual key's lifetime spend, identical for every
// account, so there is no per-account spend to show.
func TestStatusPageHasNoSpendColumn(t *testing.T) {
	page := StatusPage(true, "")

	for _, unwanted := range []string{"Spend", "key_spend"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("status page still mentions %q", unwanted)
		}
	}
}

func TestStatusPageDoesNotEmbedAnUnrequestedToken(t *testing.T) {
	// The shell echoes back only the token the caller passed in the query string.
	if page := StatusPage(true, ""); strings.Contains(page, `var token = "`) &&
		!strings.Contains(page, `var token = "";`) {
		t.Error("token literal should be empty when none was supplied")
	}
	if page := StatusPage(true, "abc123"); !strings.Contains(page, `var token = "abc123";`) {
		t.Error("a supplied token should be inlined so a copied link keeps working")
	}
}

// jsString has to survive a value that would otherwise close the script element early.
func TestJSStringEscapesScriptClose(t *testing.T) {
	got := jsString(`</script><script>alert(1)</script>`)
	if strings.Contains(got, "</script>") {
		t.Errorf("jsString = %s, want the closing tag escaped", got)
	}

	page := LoginPage(`</script><img src=x onerror=alert(1)>`)
	if strings.Contains(page, "</script><img") {
		t.Error("login page broke out of its script element")
	}
}

func TestStatusPageDisabledConsoleExplainsItself(t *testing.T) {
	page := StatusPage(false, "")
	if !strings.Contains(page, "console_token") {
		t.Error("a disabled console should name the setting that enables it")
	}
}
