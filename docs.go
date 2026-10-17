// Package ghok embeds the documentation shipped with the binary.
package ghok

import "embed"

// Docs contains the version-matched user documentation and agent skill.
//
//go:embed README.md docs/*.md skills/ghok/SKILL.md
var Docs embed.FS
