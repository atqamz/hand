// Package osfacts reads the OS facts Hand needs to check a guard record against reality:
// boot identity, process incarnation, controlling terminal, foreground group, PID-namespace
// identity, real uid, and the environ/cwd scan the attested-repair absence check needs. Hand
// trusts none of these from the guard's own claim; it re-reads them itself, per
// docs/architecture/v19-contracts/346-capability-adapters-v2.md ("Execution incarnation").
package osfacts

// Observation is Hand's classification of a re-read incarnation. A zero-value
// Incarnation always observes Unknown. The names avoid the contract's "ceased"
// record: Absent says nothing about T(G); BootChanged is strictly stronger.
type Observation string

const (
	Alive       Observation = "alive"
	Absent      Observation = "absent"
	BootChanged Observation = "boot-changed"
	Unknown     Observation = "unknown"
)
