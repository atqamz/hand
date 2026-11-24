package desktop_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/qr"
)

const phoneBase = "https://host.example"

func phoneTester(t *testing.T, m *phoneModel, dark bool) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := phoneApp(t, m)
	return app, sized(t, app, windowWidth, windowHeight, dark)
}

func mint(t *testing.T, tt *ui.Tester) {
	t.Helper()
	click(t, tt, "Address the Companion opens")
	tt.Type(phoneBase)
	click(t, tt, "Show pairing code")
	tt.Frame()
}

func inkIn(t *testing.T, tt *ui.Tester, text string) int {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	img := tt.Image()
	bg := img.At(int(r.X)-2, int(r.Y)-2)
	n := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if img.At(x, y) != bg {
				n++
			}
		}
	}
	return n
}

func mustInk(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if n := inkIn(t, tt, s); n < 12 {
			t.Errorf("%q has %d painted pixels in its box, want it drawn", s, n)
		}
	}
}

func scrollToBottom(tt *ui.Tester) {
	tt.Frame()
	tt.Scroll(600, 400, 0, 4000)
	tt.Frame()
}

func pressBottom(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	scrollToBottom(tt)
	click(t, tt, label)
	scrollToBottom(tt)
}

func goldenQR(t *testing.T) [][]bool {
	t.Helper()
	b, err := os.ReadFile("testdata/phone-qr.txt")
	if err != nil {
		t.Fatal(err)
	}
	var out [][]bool
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		row := make([]bool, len(line))
		for i, c := range line {
			row[i] = c == '#'
		}
		out = append(out, row)
	}
	return out
}

func TestThePhonePageListsPairedCompanionsWithLastSeen(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			_, tt := phoneTester(t, phoneFixture(t), dark)
			mustText(t, tt, "Fleets / golden / companion", "Connect your Companion", "Paired Companions", "Pixel 8", "Old tablet",
				"Chrome · paired Nov 20, seen 09:41", "signed out after 90 days · this fleet only · paired Aug 2",
				"golden (this fleet)", "kiln", "Revoke Pixel 8 everywhere", "Revoke Pixel 8 in kiln", "Revoke Old tablet")
			mustNotText(t, tt, "No Companion is paired.")
			scrollToBottom(tt)
			mustInk(t, tt, "Pixel 8", "Old tablet")
		})
	}
}

func TestThePhonePageWithNoCompanionsSaysSo(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			m.data.Devices = nil
			_, tt := phoneTester(t, m, dark)
			scrollToBottom(tt)
			mustText(t, tt, "No Companion is paired.")
			mustInk(t, tt, "No Companion is paired.")
		})
	}
}

func TestPairingCodeShowsWithCountdownLinkAndQR(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			_, tt := phoneTester(t, m, dark)
			mint(t, tt)
			if len(m.codeReqs) != 1 || m.codeReqs[0].Base != phoneBase || len(m.codeReqs[0].Fleets) != 0 {
				t.Fatalf("code requests = %+v", m.codeReqs)
			}
			mustText(t, tt, phoneCode, phoneLink, "5:00 left", "Copy link", "New code",
				"Scan the code with the Companion's camera, or open this link on the Companion:",
				"Or open "+phoneBase+"/"+fleetID+"/pair and type the code:")
			if _, ok := tt.Find("QR code for the pairing link"); !ok {
				t.Fatalf("no QR in %q", tt.Texts())
			}
			mustInk(t, tt, phoneCode)
		})
	}
}

func TestPairingCodeRequestNamesTheTickedFleets(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	click(t, tt, "kiln")
	mint(t, tt)
	if len(m.codeReqs) != 1 || fmt.Sprint(m.codeReqs[0].Fleets) != "["+secondID+"]" {
		t.Fatalf("code requests = %+v", m.codeReqs)
	}
	mustText(t, tt, "It pairs the Companion with golden and kiln.")
}

func TestPairingCodeWithoutAnAddressAsksForOneAndMintsNothing(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	click(t, tt, "Show pairing code")
	tt.Frame()
	mustText(t, tt, "Type the address your Companion opens, like https://host.example")
	if len(m.codeReqs) != 0 {
		t.Fatalf("a code was requested with no address: %+v", m.codeReqs)
	}
}

func TestPairingCodeCountsDownAndIsClearedWhenItExpires(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			app, tt := phoneTester(t, m, dark)
			mint(t, tt)
			*m.clock = m.clock.Add(90 * time.Second)
			tt.Advance(time.Second)
			tt.Frame()
			mustText(t, tt, "3:30 left", phoneCode, phoneLink)
			*m.clock = m.clock.Add(3*time.Minute + 30*time.Second)
			tt.Advance(time.Second)
			tt.Frame()
			mustNotText(t, tt, phoneCode)
			mustNotText(t, tt, phoneLink)
			mustNotText(t, tt, "Copy link")
			if _, ok := tt.Find("QR code for the pairing link"); ok {
				t.Fatal("the QR of an expired code is still on screen")
			}
			mustText(t, tt, "Show pairing code")
			for _, s := range tt.Texts() {
				if strings.Contains(s, phoneCode) {
					t.Fatalf("expired code left in %q", s)
				}
			}
			_ = app
		})
	}
}

func TestAnExpiredCodeFromTheBoardIsNeverShown(t *testing.T) {
	m := phoneFixture(t)
	m.data.Code = &desktop.PhoneCode{Code: phoneCode, Link: phoneLink, URL: phoneBase + "/pair", Address: phoneBase,
		Fleets: []desktop.PhoneFleetRef{{ID: fleetID, Name: "golden"}}, ExpiresAt: m.clock.Add(-time.Second).Format(time.RFC3339)}
	_, tt := phoneTester(t, m, false)
	mustNotText(t, tt, phoneCode)
	mustNotText(t, tt, phoneLink)
	if _, ok := tt.Find("QR code for the pairing link"); ok {
		t.Fatal("a QR was drawn for a code that has expired")
	}
}

func TestCopyLinkFillsTheClipboardOnlyOnTheClick(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	mint(t, tt)
	if tt.Clipboard() != "" {
		t.Fatalf("clipboard holds %q before any click", tt.Clipboard())
	}
	click(t, tt, "Copy link")
	if tt.Clipboard() != phoneLink {
		t.Fatalf("clipboard = %q, want the pairing link", tt.Clipboard())
	}
	tt.Frame()
	mustText(t, tt, "Copied")
	tt.Advance(2 * time.Second)
	tt.Frame()
	mustText(t, tt, "Copy link")
}

func TestQRBytesMatchTheQRPackageForAFixedLink(t *testing.T) {
	want := goldenQR(t)
	got, err := qr.Matrix(phoneLink)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("internal/qr no longer draws the golden matrix for the fixed link")
	}
	img, ok := desktop.QRImage(phoneLink)
	if !ok {
		t.Fatal("no image for a link that fits")
	}
	side := len(want) + 8
	if img.Bounds().Dx() != side || img.Bounds().Dy() != side {
		t.Fatalf("image is %v, want %dx%d with a 4-module quiet zone", img.Bounds(), side, side)
	}
	for y := range side {
		for x := range side {
			dark := false
			if my, mx := y-4, x-4; my >= 0 && mx >= 0 && my < len(want) && mx < len(want) {
				dark = want[my][mx]
			}
			if (img.GrayAt(x, y).Y < 128) != dark {
				t.Fatalf("module %d,%d differs from the golden matrix", x, y)
			}
		}
	}
}

func TestAnOversizedLinkMakesNoQR(t *testing.T) {
	if _, ok := desktop.QRImage(strings.Repeat("a", 300)); ok {
		t.Fatal("a link past the QR limit drew an image")
	}
}

func qrModulesOnScreen(t *testing.T, tt *ui.Tester, label string, want [][]bool) (wrong int) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	side := len(want) + 8
	mod := r.W / float32(side)
	if mod < 2 {
		t.Fatalf("modules are %.1f px wide", mod)
	}
	img := tt.Image()
	for y := range want {
		for x := range want[y] {
			px := image.Pt(int(r.X+(float32(x+4)+0.5)*mod), int(r.Y+(float32(y+4)+0.5)*mod))
			dark := false
			cr, cg, cb, _ := img.At(px.X, px.Y).RGBA()
			dark = (cr+cg+cb)/3 < 0x8000
			if dark != want[y][x] {
				wrong++
			}
		}
	}
	return wrong
}

func TestTheQRDrawnOnScreenIsTheQROfThePairingLink(t *testing.T) {
	want := goldenQR(t)
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			_, tt := phoneTester(t, m, dark)
			mint(t, tt)
			tt.Scroll(600, 400, 0, 220)
			tt.Frame()
			if wrong := qrModulesOnScreen(t, tt, "QR code for the pairing link", want); wrong != 0 {
				t.Fatalf("%d modules on screen differ from the QR of %s", wrong, "the fixed link")
			}
		})
	}
}

func TestTheQRFollowsTheLinkWhenANewCodeReplacesTheOld(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	mint(t, tt)
	tt.Scroll(600, 400, 0, 220)
	tt.Frame()
	if wrong := qrModulesOnScreen(t, tt, "QR code for the pairing link", goldenQR(t)); wrong != 0 {
		t.Fatalf("%d modules differ before the link changes", wrong)
	}
	other := phoneLink[:len(phoneLink)-1] + "Q"
	m.mu.Lock()
	m.link = other
	m.mu.Unlock()
	tt.Scroll(600, 400, 0, -4000)
	tt.Frame()
	click(t, tt, "New code")
	tt.Frame()
	mustText(t, tt, other)
	tt.Scroll(600, 400, 0, 220)
	tt.Frame()
	if wrong := qrModulesOnScreen(t, tt, "QR code for the pairing link", goldenQR(t)); wrong == 0 {
		t.Fatal("the QR still shows the old link after the link changed")
	}
}

func TestThePairingLinkIsNeverLoggedPrintedOrKeptAfterTheCodeIsGone(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	var jbuf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	jlog := slog.New(slog.NewJSONHandler(&jbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { slog.SetDefault(old) })
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	mint(t, tt)
	click(t, tt, "Copy link")
	d, err := m.Phone(nil, fleetID)
	if err != nil {
		t.Fatal(err)
	}
	slog.Info("phone", "data", d, "code", d.Code.Code, "link", d.Code.Link)
	jlog.Info("phone", "data", d, "code", d.Code.Code, "link", d.Code.Link)
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		string(raw),
		jbuf.String(),
		fmt.Sprintf("%v %+v %#v %s", d, d, d, d.Code.Link),
		fmt.Sprintf("%v %+v %#v", *d.Code, *d.Code, *d.Code),
		buf.String(),
	} {
		if strings.Contains(s, phoneCode) || strings.Contains(s, "K7QM3") || strings.Contains(s, "host.example/"+fleetID+"/pair#") {
			t.Fatalf("a secret reached output: %q", s)
		}
	}
	if !strings.Contains(buf.String(), "[hidden]") {
		t.Fatalf("the log did not mark the hidden value: %q", buf.String())
	}
	for _, a := range tt.Announcements() {
		if strings.Contains(a, phoneCode) || strings.Contains(a, phoneLink) {
			t.Fatalf("announcement %q carries the code", a)
		}
	}
	app.Show(desktop.BoardScreen)
	tt.Frame()
	for _, s := range tt.Texts() {
		if strings.Contains(s, phoneCode) || strings.Contains(s, "pair#") {
			t.Fatalf("the code outlived its page in %q", s)
		}
	}
	m.mu.Lock()
	m.data.Code = nil
	m.mu.Unlock()
	app.Show(desktop.PhoneScreen)
	tt.Frame()
	mustNotText(t, tt, phoneCode)
	mustNotText(t, tt, phoneLink)
}

func TestAHostWithoutThePhoneRoutesGetsOneSentence(t *testing.T) {
	m := phoneFixture(t)
	m.features = []string{"fleets"}
	_, tt := phoneTester(t, m, true)
	mustText(t, tt, "This host is too old for the Companion page", "Update hand on the host and restart its Board to pair and manage Companions here.")
	mustNotText(t, tt, "The board could not be read")
	mustNotText(t, tt, "Show pairing code")
	if m.reads != 0 {
		t.Fatalf("an old host was asked for the Companions %d times", m.reads)
	}
}

func TestAFailedFirstReadShowsTheErrorWithRetry(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			m.readErr = errors.New("the board did not answer in time")
			_, tt := phoneTester(t, m, dark)
			mustText(t, tt, "The board could not be read", "Retry")
			mustNotText(t, tt, "Paired Companions")
			mustInk(t, tt, "The board could not be read")
			m.mu.Lock()
			m.readErr = nil
			m.mu.Unlock()
			click(t, tt, "Retry")
			tt.Frame()
			mustText(t, tt, "Paired Companions", "Pixel 8")
			mustNotText(t, tt, "The board could not be read")
		})
	}
}

func TestAPageThatFailsAfterLoadingKeepsItsDataAndMarksItStale(t *testing.T) {
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	m.mu.Lock()
	m.readErr = errors.New("connection refused")
	m.mu.Unlock()
	for range 15 {
		clock.Advance(desktop.FocusedRefresh)
		tt.Frame()
	}
	mustText(t, tt, "Stale · retrying", "Pixel 8", "Old tablet")
	mustNotText(t, tt, "The board could not be read")
	m.mu.Lock()
	m.readErr = nil
	m.mu.Unlock()
	for range 40 {
		clock.Advance(desktop.FocusedRefresh)
		tt.Frame()
	}
	mustNotText(t, tt, "Stale · retrying")
}

func TestARevokeSendsTheExactTargetAfterAConfirmation(t *testing.T) {
	cases := []struct {
		name, press string
		want        desktop.PhoneRevokeRequest
	}{
		{"one fleet", "Revoke Pixel 8 in kiln", desktop.PhoneRevokeRequest{Phone: "ph-pixel", Fleet: secondID}},
		{"here", "Revoke Pixel 8 in golden", desktop.PhoneRevokeRequest{Phone: "ph-pixel", Fleet: fleetID}},
		{"everywhere", "Revoke Pixel 8 everywhere", desktop.PhoneRevokeRequest{Phone: "ph-pixel"}},
		{"record", "Revoke Old tablet", desktop.PhoneRevokeRequest{ID: 7}},
	}
	for _, tc := range cases {
		for _, dark := range []bool{false, true} {
			t.Run(tc.name+"/"+theme(dark), func(t *testing.T) {
				m := phoneFixture(t)
				_, tt := phoneTester(t, m, dark)
				pressBottom(t, tt, tc.press)
				if len(m.revokes) != 0 {
					t.Fatalf("the press revoked before the confirmation: %+v", m.revokes)
				}
				mustText(t, tt, "Cancel", "Revoke now")
				mustInk(t, tt, "Revoke now")
				click(t, tt, "Cancel")
				tt.Frame()
				if len(m.revokes) != 0 || tt.HasText("Revoke now") {
					t.Fatalf("cancel revoked or stayed: %+v", m.revokes)
				}
				pressBottom(t, tt, tc.press)
				click(t, tt, "Revoke now")
				tt.Frame()
				if len(m.revokes) != 1 || m.revokes[0] != tc.want {
					t.Fatalf("revokes = %+v, want exactly %+v", m.revokes, tc.want)
				}
			})
		}
	}
}

func TestARevokeUpdatesTheList(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	pressBottom(t, tt, "Revoke Old tablet")
	click(t, tt, "Revoke now")
	tt.Frame()
	mustNotText(t, tt, "Old tablet")
	mustText(t, tt, "Pixel 8")
}

func partialFailure(m *phoneModel) {
	d := m.data
	left := phoneDevices()[0]
	left.Fleets = left.Fleets[1:]
	d.Devices = []desktop.PhoneDevice{left}
	d.Revoke = &desktop.PhoneRevoked{
		Revoked: []desktop.PhoneFleetRef{{ID: fleetID, Name: "golden"}},
		Failed:  []desktop.PhoneFailed{{ID: secondID, Name: "kiln", Reason: "unreachable"}},
	}
	m.revokeOut = &d
}

func TestAPartialRevokeFailureIsStatedAndCanBeRepeated(t *testing.T) {
	const sentence = "Revoked in golden. kiln could not be reached: its record of this Companion, if it has one, is still active. Press Revoke everywhere again when it is back."
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			partialFailure(m)
			_, tt := phoneTester(t, m, dark)
			pressBottom(t, tt, "Revoke Pixel 8 everywhere")
			click(t, tt, "Revoke now")
			scrollToBottom(tt)
			mustText(t, tt, sentence, "Dismiss")
			mustInk(t, tt, sentence)
			want := desktop.PhoneRevokeRequest{Phone: "ph-pixel"}
			click(t, tt, "Revoke everywhere")
			tt.Frame()
			if len(m.revokes) != 2 || m.revokes[0] != want || m.revokes[1] != want {
				t.Fatalf("revokes = %+v, want the same request twice", m.revokes)
			}
			click(t, tt, "Dismiss")
			tt.Frame()
			mustNotText(t, tt, sentence)
		})
	}
}

func TestAPartialFailureOfSeveralFleetsUsesThePluralSentence(t *testing.T) {
	m := phoneFixture(t)
	d := m.data
	d.Revoke = &desktop.PhoneRevoked{
		Revoked: nil,
		Failed:  []desktop.PhoneFailed{{ID: fleetID, Name: "golden"}, {ID: secondID, Name: "kiln"}},
	}
	m.revokeOut = &d
	_, tt := phoneTester(t, m, false)
	pressBottom(t, tt, "Revoke Pixel 8 everywhere")
	click(t, tt, "Revoke now")
	tt.Frame()
	mustText(t, tt, "Nothing was revoked. golden and kiln could not be reached: their records of this Companion, if they have any, are still active. Press Revoke everywhere again when they are back.")
}

func TestARevokeThatFailsOutrightIsShownAndTheListStays(t *testing.T) {
	m := phoneFixture(t)
	m.revokeErr = errors.New("the board did not answer in time")
	_, tt := phoneTester(t, m, false)
	pressBottom(t, tt, "Revoke Old tablet")
	click(t, tt, "Revoke now")
	tt.Frame()
	mustText(t, tt, "Old tablet", "Pixel 8")
	var found bool
	for _, s := range tt.Texts() {
		if strings.Contains(s, "did not answer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the failure is not shown in %q", tt.Texts())
	}
}

func TestAReadStartedBeforeAnActionIsDroppedWhenItLandsAfter(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			inUTC(t)
			app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
			app.DraftDir = t.TempDir()
			app.Now = func() time.Time { return *m.clock }
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			app.Boot()
			app.Show(desktop.PhoneScreen)
			run := func(until func() bool) {
				t.Helper()
				deadline := time.After(5 * time.Second)
				for !until() {
					select {
					case fn := <-posted:
						fn()
					case <-deadline:
						t.Fatal("an answer never landed")
					}
				}
			}
			tt := sized(t, app, windowWidth, windowHeight, dark)
			run(func() bool { tt.Frame(); return tt.HasText("Paired Companions") })
			m.gate = make(chan struct{})
			m.entered = make(chan struct{}, 2)
			app.RetryPhone()
			<-m.entered
			click(t, tt, "Address the Companion opens")
			tt.Type(phoneBase)
			click(t, tt, "Show pairing code")
			run(func() bool { tt.Frame(); return tt.HasText(phoneLink) })
			m.mu.Lock()
			m.data.Code = nil
			m.mu.Unlock()
			m.gate <- struct{}{}
			deadline := time.After(200 * time.Millisecond)
			for {
				select {
				case fn := <-posted:
					fn()
					continue
				case <-deadline:
				}
				break
			}
			tt.Frame()
			mustText(t, tt, phoneLink)
		})
	}
}

func TestAnActionAnswerThatLandsAfterThePageWasLeftIsDropped(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			partialFailure(m)
			m.revokeGate = make(chan struct{})
			m.revokeEntered = make(chan struct{}, 2)
			inUTC(t)
			app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
			app.DraftDir = t.TempDir()
			app.Now = func() time.Time { return *m.clock }
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			app.Boot()
			app.Show(desktop.PhoneScreen)
			tt := sized(t, app, windowWidth, windowHeight, dark)
			deadline := time.After(5 * time.Second)
			for !tt.HasText("Paired Companions") {
				select {
				case fn := <-posted:
					fn()
				case <-deadline:
					t.Fatal("the page never loaded")
				}
				tt.Frame()
			}
			pressBottom(t, tt, "Revoke Pixel 8 everywhere")
			click(t, tt, "Revoke now")
			<-m.revokeEntered
			app.Show(desktop.BoardScreen)
			app.Show(desktop.PhoneScreen)
			m.revokeGate <- struct{}{}
			quiet := time.After(300 * time.Millisecond)
		drain:
			for {
				select {
				case fn := <-posted:
					fn()
				case <-quiet:
					break drain
				}
			}
			tt.Frame()
			scrollToBottom(tt)
			for _, s := range tt.Texts() {
				if strings.Contains(s, "could not be reached") {
					t.Fatalf("an answer for a page that was closed showed on the new page: %q", s)
				}
			}
		})
	}
}

func TestTabVisitsThePageInReadingOrderAndEnterActs(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			_, tt := phoneTester(t, m, dark)
			labels := []string{"Address the Companion opens", "kiln", "Show pairing code", "Pair the Hand app", "Revoke Pixel 8 everywhere", "Revoke Pixel 8 in golden", "Revoke Pixel 8 in kiln", "Revoke Old tablet"}
			var order []string
			for range 40 {
				tt.Key(0, ui.KeyTab)
				for _, l := range labels {
					if tt.Focused(l) && (len(order) == 0 || order[len(order)-1] != l) {
						order = append(order, l)
					}
				}
				if len(order) == len(labels) {
					break
				}
			}
			if fmt.Sprint(order) != fmt.Sprint(labels) {
				t.Fatalf("tab order = %q, want %q", order, labels)
			}
			click(t, tt, "Address the Companion opens")
			tt.Type(phoneBase)
			tt.Key(0, ui.KeyTab)
			tt.Key(0, ui.KeyTab)
			if !tt.Focused("Show pairing code") {
				t.Fatalf("focus is not on the code button")
			}
			tt.Key(0, ui.KeyEnter)
			tt.Frame()
			if len(m.codeReqs) != 1 {
				t.Fatalf("Enter on the button made %d code requests", len(m.codeReqs))
			}
		})
	}
}

func TestEscapeKeyDoesNotRevoke(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	pressBottom(t, tt, "Revoke Old tablet")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if len(m.revokes) != 0 {
		t.Fatalf("Escape revoked: %+v", m.revokes)
	}
}

func TestTheHandAppLinkShowsWithAQRAndExpires(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			m := phoneFixture(t)
			_, tt := phoneTester(t, m, dark)
			click(t, tt, "Pair the Hand app")
			tt.Frame()
			mustText(t, tt, appLink, "New app code", "5:00 left")
			if _, ok := tt.Find("QR code for the Hand app pairing link"); !ok {
				t.Fatalf("no app QR in %q", tt.Texts())
			}
			*m.clock = m.clock.Add(6 * time.Minute)
			tt.Advance(time.Second)
			tt.Frame()
			mustNotText(t, tt, appLink)
			if _, ok := tt.Find("QR code for the Hand app pairing link"); ok {
				t.Fatal("the app QR outlived its code")
			}
		})
	}
}

func TestTheHandAppSectionExplainsAnIrohThatIsNotReady(t *testing.T) {
	m := phoneFixture(t)
	m.data.Iroh = &desktop.PhoneIroh{State: "off", Tone: "neutral", Text: "Companion access over iroh is off."}
	_, tt := phoneTester(t, m, false)
	mustText(t, tt, "Pairing the Hand app needs the iroh sidecar ready with a relay. Companion access over iroh is off.")
	mustNotText(t, tt, "Pair the Hand app")
}

func TestAnUnreachableFleetIsNamedAboveTheList(t *testing.T) {
	m := phoneFixture(t)
	m.data.Unreachable = []string{"kiln"}
	_, tt := phoneTester(t, m, false)
	mustText(t, tt, "kiln could not be reached, so its Companions are not listed here.")
}

func TestAPlainHTTPAddressCarriesItsWarning(t *testing.T) {
	const warning = "This address is plain HTTP on a network. The code and the Companion's session cross it unencrypted unless the tunnel encrypts them, as SSH and WireGuard do."
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	mint(t, tt)
	mustNotText(t, tt, warning)
	m2 := phoneFixture(t)
	m2.plain = true
	_, tt2 := phoneTester(t, m2, true)
	mint(t, tt2)
	mustText(t, tt2, warning)
}

func TestTheCompanionRailItemOpensThePhonePage(t *testing.T) {
	m := phoneFixture(t)
	inUTC(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
	app.DraftDir = t.TempDir()
	app.Now = func() time.Time { return *m.clock }
	app.Boot()
	tt := sized(t, app, windowWidth, windowHeight, false)
	click(t, tt, "Companion")
	tt.Frame()
	if app.Screen != desktop.PhoneScreen {
		t.Fatalf("screen = %v, want the Companion page", app.Screen)
	}
	mustText(t, tt, "Connect your Companion", "Pixel 8")
}

func TestScenesTheCompanionPage(t *testing.T) {
	cases := map[string]func(t *testing.T, m *phoneModel, tt *ui.Tester){
		"115-companion-code": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			mint(t, tt)
			mustInk(t, tt, phoneCode, "Copy link")
		},
		"116-companion-qr": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			mint(t, tt)
			tt.Scroll(600, 400, 0, 220)
			tt.Frame()
			if wrong := qrModulesOnScreen(t, tt, "QR code for the pairing link", goldenQR(t)); wrong != 0 {
				t.Fatalf("%d modules differ", wrong)
			}
		},
		"117-companion-devices": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			scrollToBottom(tt)
			mustInk(t, tt, "Pixel 8", "Old tablet")
		},
		"118-companion-revoke-partial": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			partialFailure(m)
			pressBottom(t, tt, "Revoke Pixel 8 everywhere")
			click(t, tt, "Revoke now")
			scrollToBottom(tt)
			mustInk(t, tt, "Dismiss")
		},
		"119-companion-empty": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			m.data.Devices = nil
			scrollToBottom(tt)
		},
		"120-companion-error": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			mustInk(t, tt, "The board could not be read", "Retry")
		},
		"121-companion-old": func(t *testing.T, m *phoneModel, tt *ui.Tester) {
			mustInk(t, tt, "This host is too old for the Companion page")
		},
	}
	for base, step := range cases {
		for _, dark := range []bool{false, true} {
			name := base + "-1100x760-" + theme(dark)
			t.Run(name, func(t *testing.T) {
				m := phoneFixture(t)
				switch base {
				case "119-companion-empty":
					m.data.Devices = nil
				case "120-companion-error":
					m.readErr = errors.New("the board did not answer in time")
				case "121-companion-old":
					m.features = []string{"fleets"}
				}
				_, tt := phoneTester(t, m, dark)
				step(t, m, tt)
				shotSized(t, tt, name, windowWidth, windowHeight)
			})
		}
	}
}

func TestLeavingThePageDropsTheCodeAndTheQR(t *testing.T) {
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	mint(t, tt)
	if !desktop.PhoneHeld(app) {
		t.Fatal("no code held after minting")
	}
	app.Show(desktop.BoardScreen)
	if desktop.PhoneHeld(app) {
		t.Fatal("the code or its QR is still held after the page was left")
	}
}

func TestAnExpiredCodeInAReplyLeavesNoQRBehind(t *testing.T) {
	m := phoneFixture(t)
	m.data.Code = &desktop.PhoneCode{Code: phoneCode, Link: phoneLink, URL: phoneBase + "/pair", Address: phoneBase,
		Fleets: []desktop.PhoneFleetRef{{ID: fleetID, Name: "golden"}}, ExpiresAt: m.clock.Add(-time.Second).Format(time.RFC3339)}
	m.data.App = &desktop.PhoneApp{Link: appLink, ExpiresAt: m.clock.Add(-time.Second).Format(time.RFC3339)}
	app, _ := phoneTester(t, m, false)
	if desktop.PhoneHeld(app) {
		t.Fatal("an expired reply left a code, link or QR in memory")
	}
}

func TestCopyLinkAfterTheCodeExpiredWritesNothing(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	mint(t, tt)
	*m.clock = m.clock.Add(10 * time.Minute)
	click(t, tt, "Copy link")
	if tt.Clipboard() != "" {
		t.Fatalf("clipboard = %q after the code expired", tt.Clipboard())
	}
}

func TestAStalePageOffersRetryThatReadsAtOnce(t *testing.T) {
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	m.mu.Lock()
	m.readErr = errors.New("connection refused")
	m.mu.Unlock()
	for range 15 {
		clock.Advance(desktop.FocusedRefresh)
		tt.Frame()
	}
	mustText(t, tt, "Stale · retrying", "Retry", "Pixel 8")
	m.mu.Lock()
	m.readErr = nil
	m.mu.Unlock()
	click(t, tt, "Retry")
	tt.Frame()
	mustNotText(t, tt, "Stale · retrying")
	mustText(t, tt, "Pixel 8")
}

func TestAFailedFirstReadBacksOffBeforeReadingAgain(t *testing.T) {
	m := phoneFixture(t)
	m.readErr = errors.New("the board did not answer in time")
	app, tt := phoneTester(t, m, false)
	clock := newFakeClock()
	app.Clock = clock
	app.StartRefresh()
	m.mu.Lock()
	before := m.reads
	m.mu.Unlock()
	for range 200 {
		clock.Advance(desktop.FocusedRefresh)
		tt.Frame()
	}
	m.mu.Lock()
	n := m.reads - before
	m.mu.Unlock()
	if n > 12 {
		t.Fatalf("%d reads while the board did not answer; the page does not back off", n)
	}
	mustText(t, tt, "The board could not be read", "Retry")
}

func TestARevokeInOneFleetKeepsTheCompanionInTheOthers(t *testing.T) {
	m := phoneFixture(t)
	_, tt := phoneTester(t, m, false)
	pressBottom(t, tt, "Revoke Pixel 8 in kiln")
	click(t, tt, "Revoke now")
	tt.Frame()
	scrollToBottom(tt)
	mustText(t, tt, "Pixel 8")
	if _, ok := tt.Find("Revoke Pixel 8 in golden"); !ok {
		t.Fatal("Pixel 8 lost its access to golden")
	}
	if _, ok := tt.Find("Revoke Pixel 8 in kiln"); ok {
		t.Fatal("Pixel 8 still has access to kiln")
	}
}

func TestAnEmptyListWithAnUnreachableFleetDoesNotClaimNoneArePaired(t *testing.T) {
	m := phoneFixture(t)
	m.data.Devices = nil
	m.data.Unreachable = []string{"kiln"}
	_, tt := phoneTester(t, m, false)
	mustText(t, tt, "No Companion was found in the fleets that could be read.")
	mustNotText(t, tt, "No Companion is paired.")
}

func codeOnScreen(tt *ui.Tester) bool {
	if _, ok := tt.Find("QR code for the pairing link"); ok {
		return true
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, phoneCode) || strings.Contains(s, "pair#") || s == "Copy link" {
			return true
		}
	}
	return false
}

func TestOpeningAPageOverTheCompanionPageDropsTheCodeLinkAndQR(t *testing.T) {
	cases := []struct {
		name string
		open func(*desktop.App)
		back bool
	}{
		{"ref", func(a *desktop.App) { a.OpenRef("t1") }, true},
		{"link", func(a *desktop.App) { _ = a.OpenLink("handboard://open/" + fleetID + "/task/t1") }, false},
		{"doc", func(a *desktop.App) { a.OpenDoc("README.md") }, true},
	}
	for _, tc := range cases {
		for _, dark := range []bool{false, true} {
			t.Run(tc.name+"/"+theme(dark), func(t *testing.T) {
				m := phoneFixture(t)
				app, tt := phoneTester(t, m, dark)
				mint(t, tt)
				if !codeOnScreen(tt) {
					t.Fatal("no code on screen after minting")
				}
				m.mu.Lock()
				m.readErr = errors.New("the board did not answer in time")
				m.mu.Unlock()
				tc.open(app)
				if app.Screen == desktop.PhoneScreen {
					t.Fatalf("%s did not leave the Companion page", tc.name)
				}
				if desktop.PhoneHeld(app) {
					t.Fatal("the code, link or QR is still held under the opened page")
				}
				tt.Frame()
				if codeOnScreen(tt) {
					t.Fatal("the code, link or QR shows with a page open over the Companion page")
				}
				if !tc.back {
					return
				}
				app.CloseViewer()
				tt.Frame()
				if desktop.PhoneHeld(app) || codeOnScreen(tt) {
					t.Fatal("the closed viewer brought the code, link or QR back with no read")
				}
				mustText(t, tt, "The board could not be read")
			})
		}
	}
}

func TestClosingTheViewerReadsTheCompanionPageAgain(t *testing.T) {
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	reads := m.reads
	app.OpenRef("t1")
	app.CloseViewer()
	tt.Frame()
	if m.reads <= reads {
		t.Fatal("the page was not read again when the viewer closed")
	}
	mustText(t, tt, "Pixel 8")
}

func TestSwitchingFleetDropsTheCompanionPageData(t *testing.T) {
	m := phoneFixture(t)
	app, tt := phoneTester(t, m, false)
	mint(t, tt)
	if !desktop.PhoneHeld(app) {
		t.Fatal("no code held after minting")
	}
	m.mu.Lock()
	m.readErr = errors.New("the board did not answer in time")
	m.mu.Unlock()
	app.SelectFleet(secondID)
	if desktop.PhoneHeld(app) {
		t.Fatal("the first fleet's code is held after switching fleet")
	}
	tt.Frame()
	if codeOnScreen(tt) {
		t.Fatal("the first fleet's code shows after switching fleet")
	}
}
