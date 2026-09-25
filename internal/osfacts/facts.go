// Package osfacts reads the OS facts Hand needs to check a guard record against reality:
// boot identity, process incarnation, controlling terminal, foreground group, PID-namespace
// identity, real uid, and the environ/cwd scan the attested-repair absence check needs. Hand
// trusts none of these from the guard's own claim; it re-reads them itself, per
// docs/architecture/v19-contracts/346-capability-adapters-v2.md ("Execution incarnation").
package osfacts

// Observation is Hand's classification of a re-read incarnation against a previously
// recorded one. A zero-value Incarnation always observes Unknown, never Ceased.
type Observation string

const (
	Alive   Observation = "alive"
	Ceased  Observation = "ceased"
	Unknown Observation = "unknown"
)
