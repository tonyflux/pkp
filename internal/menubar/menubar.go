// Package menubar is the native side of PKP's menu bar item (Menu Bar
// Presence): the monochrome template icon, the attention dot and readable
// name on the item, the panel kept anchored under it, clicks outside the
// app that close the panel, and telling a quit the system asks for (log
// out, restart, shut down) from one the user asks for.
package menubar

import _ "embed"

// Icon is the menu bar glyph, black on transparent at 44×44 for a 22pt bar
// at 2x. It is a template image: macOS tints it for the bar it sits in,
// light or dark. template.svg is its source.
//
//go:embed template.png
var Icon []byte

// IconOnDark is Icon in white, for trays that do not tint a template image
// (Windows, Linux) when their taskbar is dark; Icon serves the light one.
//
//go:embed glyph-white.png
var IconOnDark []byte
