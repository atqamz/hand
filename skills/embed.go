// Package skills embeds the first-party secondhand Agent Skill's canonical source tree at
// build time, so an installed hand binary can install and reconcile it without network access.
// internal/skill owns installation and ownership logic; this package only owns the embed.
package skills

import "embed"

//go:embed secondhand
var Secondhand embed.FS
