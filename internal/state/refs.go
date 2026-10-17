package state

import "strconv"

func TaskRef(id int64) string { return "t" + strconv.FormatInt(id, 10) }

func DecisionRef(id int64) string { return "d" + strconv.FormatInt(id, 10) }

func AttemptRef(id int64) string { return "a" + strconv.FormatInt(id, 10) }

func ReportRef(id int64) string { return "r" + strconv.FormatInt(id, 10) }

func TimerRef(id int64) string { return "w" + strconv.FormatInt(id, 10) }
