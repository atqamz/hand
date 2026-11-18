package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAnUnpaddedInboxCounterSortsNumerically(t *testing.T) {
	defer func(old int) { shotBudget = old }(shotBudget)
	shotBudget = 1000
	a := &App{shots: map[string]*shotState{
		"20261101-050000-10-a.png": {pix: 500},
	}}
	if got := a.shotRoom("20261101-050000-2-a.png"); got != 500 {
		t.Fatalf("room for the older -2- picture is %d, want 500: the -10- picture came later in the same second and holds 500", got)
	}
}

func TestASameSecondCounterOrdersByNumber(t *testing.T) {
	defer func(old int) { shotBudget = old }(shotBudget)
	shotBudget = 1000
	a := &App{shots: map[string]*shotState{
		"20261101-050000-2-a.png":  {pix: 300},
		"20261101-050000-10-b.png": {pix: 500},
	}}
	for name, want := range map[string]int64{
		"20261101-050000-1-c.png":  200,
		"20261101-050000-2-a.png":  200,
		"20261101-050000-9-e.png":  500,
		"20261101-050000-10-b.png": 500,
		"20261101-050000-11-g.png": 1000,
	} {
		if got := a.shotRoom(name); got != want {
			t.Errorf("room for %s is %d, want %d", name, got, want)
		}
	}
}

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

func TestHoldShotEvictsTheLowestCounterNotTheLowestText(t *testing.T) {
	defer func(old int) { shotBudget = old }(shotBudget)
	shotBudget = 1000
	a := &App{shots: map[string]*shotState{
		"20261101-050000-10-a.png": {bmp: &ui.Bitmap{}, pix: 500},
		"20261101-050000-2-a.png":  {bmp: &ui.Bitmap{}, pix: 500},
	}}
	st := &shotState{}
	a.holdShot("20261101-050000-11-a.png", st, &ui.Bitmap{}, 500)
	if a.shots["20261101-050000-2-a.png"].bmp != nil {
		t.Fatal("the -2- picture is the oldest and must be evicted")
	}
	if a.shots["20261101-050000-10-a.png"].bmp == nil {
		t.Fatal("the -10- picture is newer than -2- and must stay")
	}
	if st.bmp == nil {
		t.Fatal("the new picture is not held")
	}
}

func TestShotOrderIsATotalOrder(t *testing.T) {
	names := []string{
		"",
		"x",
		"20261101-050000-2-a.png",
		"20261101-050000-02-a.png",
		"20261101-050000-10-a.png",
		"20261101-050000-1-a",
		"20261101-050000-1-b",
		"20261101-050000-15999999999999999999-a.png",
		"20261101-050001-1-a.png",
		"20261031-235959-99-a.png",
		"2026110-050000-1-a.png",
	}
	for _, a := range names {
		if shotOrder(a, a) != 0 {
			t.Errorf("%q is not equal to itself", a)
		}
		for _, b := range names {
			if sign(shotOrder(a, b)) != -sign(shotOrder(b, a)) {
				t.Errorf("order of %q and %q is not antisymmetric", a, b)
			}
			if a != b && shotOrder(a, b) == 0 {
				t.Errorf("%q and %q tie", a, b)
			}
			for _, c := range names {
				if shotOrder(a, b) <= 0 && shotOrder(b, c) <= 0 && shotOrder(a, c) > 0 {
					t.Errorf("%q <= %q <= %q but %q > %q", a, b, c, a, c)
				}
			}
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
