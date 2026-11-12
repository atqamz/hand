package kit

type Role struct{ Size, Line float32 }

var (
	Display = Role{22, 30}
	Heading = Role{18, 24}
	Title   = Role{15, 22}
	Body    = Role{14, 20}
	Meta    = Role{12, 18}
)

var Message = Role{15, 24}

const Measure = 600
