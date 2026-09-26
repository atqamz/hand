package state

import "strconv"

func TaskRef(id int64) string { return "t" + strconv.FormatInt(id, 10) }
