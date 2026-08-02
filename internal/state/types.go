// Package state is the task-level API over hand's machine state, which
// internal/store keeps in sqlite, plus the plain-text report channel at
// state/<id>.status that hand only ever reads.
package state

import "github.com/atqamz/secondhand/internal/store"

const (
	KindShip  = store.KindShip
	KindScout = store.KindScout
)

const (
	HoldKindOperator = store.HoldKindOperator
	HoldKindBlocked  = store.HoldKindBlocked
)

// Aliases rather than separate structs: the store owns the columns these map
// to, and a second definition would be one more place to forget a field.
type (
	Herdr = store.Herdr
	Task  = store.Task
	Hold  = store.Hold
)
