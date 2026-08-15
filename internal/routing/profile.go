// Package routing owns operator-defined execution profiles and task routes.
package routing

import "slices"

type TaskKind string

const (
	TaskKindScout TaskKind = "scout"
	TaskKindShip  TaskKind = "ship"
)

var taskKinds = []TaskKind{TaskKindScout, TaskKindShip}

func TaskKinds() []TaskKind {
	return slices.Clone(taskKinds)
}

type ExecutionClass string

const (
	ExecutionClassMechanical ExecutionClass = "mechanical"
	ExecutionClassStandard   ExecutionClass = "standard"
	ExecutionClassDeep       ExecutionClass = "deep"
)

var executionClasses = []ExecutionClass{
	ExecutionClassMechanical,
	ExecutionClassStandard,
	ExecutionClassDeep,
}

func ExecutionClasses() []ExecutionClass {
	return slices.Clone(executionClasses)
}

type RoutingSource string

const (
	RoutingSourceExplicitProfile RoutingSource = "explicit-profile"
	RoutingSourceRoute           RoutingSource = "route"
	RoutingSourceLegacy          RoutingSource = "legacy"
)

type Profile struct {
	Name    string
	Harness string
	Model   string
	Effort  string
}

type Route struct {
	Kind           TaskKind
	ExecutionClass ExecutionClass
	Profile        string
}

type ConfigProblem struct {
	Code           ConfigProblemCode
	Kind           TaskKind
	ExecutionClass ExecutionClass
	Profile        string
	Message        string
}

type ConfigProblemCode string

const (
	ConfigProblemMissingRoute       ConfigProblemCode = "missing-route"
	ConfigProblemDanglingRoute      ConfigProblemCode = "dangling-route"
	ConfigProblemMalformedProfile   ConfigProblemCode = "malformed-profile"
	ConfigProblemMalformedRoute     ConfigProblemCode = "malformed-route"
	ConfigProblemUnsupportedHarness ConfigProblemCode = "unsupported-harness"
	ConfigProblemUnsupportedModel   ConfigProblemCode = "unsupported-model"
	ConfigProblemUnsupportedEffort  ConfigProblemCode = "unsupported-effort"
)

type Config struct {
	Profiles []Profile
	Routes   []Route
	Problems []ConfigProblem
}
