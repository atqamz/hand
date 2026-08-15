// Package state is the task-level API over hand's machine state, which
// internal/store keeps in sqlite, plus the plain-text report channel at
// state/<id>.status that hand only ever reads.
package state

import "github.com/atqamz/hand/internal/store"

const (
	KindShip  = store.KindShip
	KindScout = store.KindScout
)

const (
	HoldKindOperator = store.HoldKindOperator
	HoldKindBlocked  = store.HoldKindBlocked
	HoldKindLimit    = store.HoldKindLimit
)

// Aliases rather than separate structs: the store owns the columns these map
// to, and a second definition would be one more place to forget a field.
type (
	Herdr            = store.Herdr
	HerdrOwnership   = store.HerdrOwnership
	Task             = store.Task
	Attempt          = store.Attempt
	TaskHistory      = store.TaskHistory
	SendAttempt      = store.SendAttempt
	Hold             = store.Hold
	TaskLifecycle    = store.TaskLifecycle
	AttemptLifecycle = store.AttemptLifecycle
	SendState        = store.SendState
	SendOrigin       = store.SendOrigin
)

var (
	ErrLifecycleConflict     = store.ErrLifecycleConflict
	ErrOwnershipConflict     = store.ErrOwnershipConflict
	ErrSendOwnershipConflict = store.ErrSendOwnershipConflict
	ErrSendInFlight          = store.ErrSendInFlight
	ErrInvalidSendTransition = store.ErrInvalidSendTransition
)

const (
	TaskOpen     = store.TaskOpen
	TaskTerminal = store.TaskTerminal
)

const (
	AttemptProvisioning = store.AttemptProvisioning
	AttemptRunning      = store.AttemptRunning
	AttemptCompleted    = store.AttemptCompleted
	AttemptFailed       = store.AttemptFailed
	AttemptInterrupted  = store.AttemptInterrupted

	SendPending      = store.SendPending
	SendNotSubmitted = store.SendNotSubmitted
	SendSubmitted    = store.SendSubmitted
	SendUncertain    = store.SendUncertain

	SendOriginOperator          = store.SendOriginOperator
	SendOriginUsageLimitResume  = store.SendOriginUsageLimitResume
	SendOriginLegacyUndelivered = store.SendOriginLegacyUndelivered
)

const (
	TeardownResourceReleasing               = store.TeardownResourceReleasing
	TeardownResourceReleased                = store.TeardownResourceReleased
	TeardownResourceAmbiguous               = store.TeardownResourceAmbiguous
	TeardownResourceRetryable               = store.TeardownResourceRetryable
	TeardownCompletionPending               = store.TeardownCompletionPending
	TeardownCompletionAppended              = store.TeardownCompletionAppended
	TeardownDispositionCompleted            = store.TeardownDispositionCompleted
	TeardownDispositionCompletedSafeDirt    = store.TeardownDispositionCompletedSafeDirt
	TeardownDispositionForced               = store.TeardownDispositionForced
	TeardownDispositionNeverLaunched        = store.TeardownDispositionNeverLaunched
	TeardownDispositionLaunchedProvisioning = store.TeardownDispositionLaunchedProvisioning
)
