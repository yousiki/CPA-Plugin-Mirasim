// Package ui holds the self-contained HTML for the two browser pages. No external
// assets, so the pages work behind a tunnel and under a restrictive CSP.
package ui

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
button.armed { border-color: #dc2626; background: color-mix(in srgb, #dc2626 18%, Canvas); }
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
// encoding/json escapes <, > and & to \u00XX, so an embedded "</script>" cannot close the
// element early.
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
