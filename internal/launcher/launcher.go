package launcher

type State string

const (
	OK        State = "ok"
	Missing   State = "missing"
	Elsewhere State = "elsewhere"
	Foreign   State = "foreign"
)
