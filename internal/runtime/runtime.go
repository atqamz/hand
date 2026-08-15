package runtime

import (
	"context"
	"errors"
)

type ErrorKind string

const (
	ErrorUsage        ErrorKind = "usage"
	ErrorPrecondition ErrorKind = "precondition"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

func Usage(err error) error { return classify(ErrorUsage, err) }

func Precondition(err error) error { return classify(ErrorPrecondition, err) }

type warningError struct {
	err      error
	warnings []string
}

func (e *warningError) Error() string { return e.err.Error() }

func (e *warningError) Unwrap() error { return e.err }

func (e *warningError) RuntimeWarnings() []string { return append([]string(nil), e.warnings...) }

func WithWarnings(err error, warnings []string) error {
	if err == nil || len(warnings) == 0 {
		return err
	}
	return &warningError{err: err, warnings: append([]string(nil), warnings...)}
}

func Warnings(err error) []string {
	var carrier interface{ RuntimeWarnings() []string }
	if !errors.As(err, &carrier) {
		return nil
	}
	return carrier.RuntimeWarnings()
}

func classify(kind ErrorKind, err error) error {
	if err == nil {
		return nil
	}
	if classified, ok := err.(*Error); ok && classified.Kind == kind {
		return err
	}
	return &Error{Kind: kind, Err: err}
}

type Result struct {
	ID             string
	Attempt        string
	Project        string
	Kind           string
	Was            string
	ExecutionClass string
	Profile        string
	RoutingSource  string
	PlannedAgainst string
	Harness        string
	Model          string
	Effort         string
	Worktree       string
	Outcome        string
	Detail         string
	Warnings       []string
	Help           []string
}

type SpawnRequest struct {
	Home            string
	ID              string
	Project         string
	Kind            string
	Profile         string
	ProfileFromFlag bool
	Harness         string
	HarnessFromFlag bool
	Model           string
	ModelFromFlag   bool
	Effort          string
	EffortFromFlag  bool
	SkipGateCheck   bool
}

type ReopenRequest struct {
	Home            string
	ID              string
	Profile         string
	ProfileFromFlag bool
	Harness         string
	HarnessFromFlag bool
	Model           string
	ModelFromFlag   bool
	Effort          string
	EffortFromFlag  bool
	SkipGateCheck   bool
}

type PromoteRequest struct {
	Home            string
	ID              string
	Profile         string
	ProfileFromFlag bool
	Harness         string
	HarnessFromFlag bool
	Model           string
	ModelFromFlag   bool
	Effort          string
	EffortFromFlag  bool
	SkipGateCheck   bool
}

type TeardownRequest struct {
	Context context.Context
	Home    string
	ID      string
	Force   bool
}
