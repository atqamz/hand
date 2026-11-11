package board

func SetAPIEventLimits(max, perTick int64) func() {
	oldMax, oldTick := apiEventMax.Swap(max), apiEventsPerTick.Swap(perTick)
	return func() { apiEventMax.Store(oldMax); apiEventsPerTick.Store(oldTick) }
}
