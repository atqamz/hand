package launcher

func Sync(func(string) string, string) (string, error) { return "", nil }

func Remove(func(string) string) (string, error) { return "", nil }

func Check(func(string) string, string) (State, string) { return Missing, "" }
