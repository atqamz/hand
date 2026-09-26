package state

import "strconv"

func TaskRef(id int64) string { return "t" + strconv.FormatInt(id, 10) }

func DecisionRef(id int64) string { return "d" + strconv.FormatInt(id, 10) }

func AttemptRef(id int64) string { return "a" + strconv.FormatInt(id, 10) }
