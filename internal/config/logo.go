package config

// Logo is the icon the management panel shows on the Mirasim login card and in the
// plugin list.
//
// It is the mirasim.ai mark (https://mirasim.ai/site/mirasim-mark-white.png) redrawn as
// vector geometry — eight rounded bars, measured from the original 793x698 artwork —
// in white on the brand purple (#6D4AE8) taken from mirasim.ai.
//
// Three constraints shaped this:
//
//   - The panel renders a plugin logo as a plain <img> with no light/dark variant
//     (PluginOAuthIcon in the management center), so the white-on-transparent PNG would
//     be invisible on the light theme. Its own background solves that for both themes.
//   - CPA HTML-escapes plugin metadata before returning it
//     (internal/api/handlers/management/plugins.go), which would mangle a raw SVG data
//     URI. Base64 contains nothing that escaping touches.
//   - Inlining it means the icon needs no request to mirasim.ai from the operator's
//     browser, which matters behind a tunnel or on a restricted network.
const Logo = "data:image/svg+xml;base64," +
	"PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxMjggMTI4" +
	"Ij48cmVjdCB3aWR0aD0iMTI4IiBoZWlnaHQ9IjEyOCIgcng9IjI4IiBmaWxsPSIjNkQ0QUU4Ii8+PGcg" +
	"dHJhbnNmb3JtPSJ0cmFuc2xhdGUoMjIgMjcuMDMpIHNjYWxlKDAuMTA1OTI3KSIgZmlsbD0iI2ZmZiI+" +
	"PHJlY3QgeD0iMSIgeT0iMCIgd2lkdGg9IjU1IiBoZWlnaHQ9IjY5OCIgcng9IjI3LjUiLz48cmVjdCB4" +
	"PSI5NyIgeT0iOTMiIHdpZHRoPSI1NSIgaGVpZ2h0PSI1MDYiIHJ4PSIyNy41Ii8+PHJlY3QgeD0iMTkz" +
	"IiB5PSIyNDkiIHdpZHRoPSI1NSIgaGVpZ2h0PSIzMDAiIHJ4PSIyNy41Ii8+PHJlY3QgeD0iMjg5IiB5" +
	"PSI0MTIiIHdpZHRoPSI1NSIgaGVpZ2h0PSI5OSIgcng9IjI3LjUiLz48cmVjdCB4PSI0NDkiIHk9IjQx" +
	"MiIgd2lkdGg9IjU1IiBoZWlnaHQ9Ijk5IiByeD0iMjcuNSIvPjxyZWN0IHg9IjU0NSIgeT0iMjQ5IiB3" +
	"aWR0aD0iNTUiIGhlaWdodD0iMzAwIiByeD0iMjcuNSIvPjxyZWN0IHg9IjY0MSIgeT0iOTciIHdpZHRo" +
	"PSI1NSIgaGVpZ2h0PSI1MDIiIHJ4PSIyNy41Ii8+PHJlY3QgeD0iNzM3IiB5PSIwIiB3aWR0aD0iNTUi" +
	"IGhlaWdodD0iNjk4IiByeD0iMjcuNSIvPjwvZz48L3N2Zz4="
