package desktop

import "testing"

func TestAPictureRoomIsTheBudgetLessWhatNewerPicturesHold(t *testing.T) {
	defer func(old int) { shotBudget = old }(shotBudget)
	shotBudget = 1000
	a := &App{shots: map[string]*shotState{
		"20261030-045800-1-a.png": {pix: 300},
		"20261102-045800-1-b.png": {pix: 200},
	}}
	for name, want := range map[string]int64{
		"20261031-045800-1-x.png": 800,
		"20261101-045800-1-x.png": 800,
		"20261103-045800-1-x.png": 1000,
		"20261029-045800-1-x.png": 500,
	} {
		if got := a.shotRoom(name); got != want {
			t.Errorf("room for %s is %d, want %d", name, got, want)
		}
	}
}
