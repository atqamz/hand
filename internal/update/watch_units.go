package update

type WatchUnitProblem struct {
	Name, Home string
	Missing    bool
	Fixable    bool
}
