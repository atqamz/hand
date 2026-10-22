package board

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func pairBoard(t *testing.T, token string) (*Board, *state.Store, string) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f, err := st.CreateFleet(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return New(st, token, Options{Base: "/" + f.ID}).(*Board), st, f.ID
}

func grant(fleets ...string) *Redeemed {
	return &Redeemed{fleets: fleets, done: map[string]bool{}, secret: strings.Repeat("a", 64), name: "Pixel", login: "operator@example.com", label: "0123456789abcdef"}
}

func TestAGrantPairsOnlyTheFleetsOfItsCodeAndOnlyOnceEach(t *testing.T) {
	ctx := context.Background()
	a, sa, aid := pairBoard(t, strings.Repeat("1", 32))
	c, sc, _ := pairBoard(t, strings.Repeat("2", 32))
	rd := grant(aid)
	if _, err := c.PairPhone(ctx, rd); !errors.Is(err, errNotInCode) {
		t.Fatalf("a fleet outside the code = %v", err)
	}
	if list, _ := sc.Devices(ctx); len(list) != 0 {
		t.Fatalf("a fleet outside the code holds %v", list)
	}
	got, err := a.PairPhone(ctx, rd)
	if err != nil || got.ID != aid || got.Name != "test" || got.Device != 1 {
		t.Fatalf("PairPhone = %+v, %v", got, err)
	}
	if _, err := a.PairPhone(ctx, rd); !errors.Is(err, errNotInCode) {
		t.Fatalf("the same grant twice = %v", err)
	}
	list, _ := sa.Devices(ctx)
	if len(list) != 1 || list[0].Phone != "0123456789abcdef" || list[0].Login != "operator@example.com" {
		t.Fatalf("records = %+v", list)
	}
	if _, ok, err := sa.DeviceWithSecret(ctx, 1, a.secretHash(rd.secret)); err != nil || !ok {
		t.Fatalf("the fleet's own hash does not open its record: %v, %v", ok, err)
	}
	if a.secretHash(rd.secret) == c.secretHash(rd.secret) {
		t.Fatal("two fleets hash one secret to one value")
	}
	if _, ok, _ := sa.DeviceWithSecret(ctx, 1, c.secretHash(rd.secret)); ok {
		t.Fatal("another fleet's hash opens the record")
	}
}

func TestAGrantNobodyRedeemedPairsNothing(t *testing.T) {
	ctx := context.Background()
	a, sa, _ := pairBoard(t, strings.Repeat("1", 32))
	var none *Redeemed
	for what, rd := range map[string]*Redeemed{"nil": none, "empty": {}} {
		if _, err := a.PairPhone(ctx, rd); !errors.Is(err, errNotInCode) {
			t.Errorf("%s grant = %v", what, err)
		}
	}
	if list, _ := sa.Devices(ctx); len(list) != 0 {
		t.Fatalf("records = %v", list)
	}
}

func TestOneGrantNamesEachFleetOnceUnderRacingCalls(t *testing.T) {
	ctx := context.Background()
	a, sa, aid := pairBoard(t, strings.Repeat("1", 32))
	rd := grant(aid)
	var wg sync.WaitGroup
	var mu sync.Mutex
	paired := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.PairPhone(ctx, rd); err == nil {
				mu.Lock()
				paired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if list, _ := sa.Devices(ctx); paired != 1 || len(list) != 1 {
		t.Fatalf("%d calls paired and %d records exist", paired, len(list))
	}
}

func TestACodeKeepsItsOwnCopyOfTheFleetSet(t *testing.T) {
	a, _, aid := pairBoard(t, strings.Repeat("1", 32))
	set := []string{aid, "f000000000001"}
	code, until := a.mint(time.Now(), set)
	set[1] = "f0000000000ff"
	res, got := a.redeem("100.64.0.1", code, until.Add(-time.Minute))
	if res != pairOK || len(got) != 2 || got[1] != "f000000000001" {
		t.Fatalf("redeem = %v, %v", res, got)
	}
	got[1] = "f0000000000ee"
	if again, _ := a.redeem("100.64.0.1", code, until.Add(-time.Minute)); again != pairUsed {
		t.Fatalf("second redeem = %v", again)
	}
}
