package desktop_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	attachImage = "login-redirect-safari.png"
	attachLog   = "server-2026-10-15.log"
	sentImage   = "20261101-045800-1-" + attachImage
	sentLog     = "20261101-045800-2-" + attachLog
)

func fileOf(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sparseOf(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	return path
}

func twoFiles(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	return dir, fileOf(t, dir, attachImage, 212<<10), fileOf(t, dir, attachLog, 1468006)
}

func drainPosts(t *testing.T, posted chan func()) {
	t.Helper()
	for {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

func asyncApp(t *testing.T, f *fixture) (*desktop.App, chan func()) {
	t.Helper()
	app := newApp(t, f)
	posted := make(chan func(), 64)
	app.Post = func(fn func()) { posted <- fn }
	return app, posted
}

func sentFixture(t *testing.T) *fixture {
	t.Helper()
	f := docFixture(t)
	blue, red, green := color.RGBA{0x40, 0x7a, 0xcc, 255}, color.RGBA{0xc4, 0x2b, 0x32, 255}, color.RGBA{0x1a, 0x7a, 0x3e, 255}
	f.inbox = map[string][]byte{sentImage: stripes(t, 480, 270, blue, red, green), sentLog: make([]byte, 1468006)}
	f.docs["inbox/"+sentImage] = desktop.Doc{Path: "inbox/" + sentImage, Name: sentImage, Kind: "image", Size: 6000, At: at("1")}
	f.docBytes["inbox/"+sentImage] = f.inbox[sentImage]
	href := func(name string) string { return "/" + fleetID + "/doc/inbox/" + name }
	tl := f.timeline[fleetID]
	tl.Entries = append([]desktop.Entry{{
		Role: "operator", At: "2026-11-01T04:58:00Z", Delivery: "delivered", Delivered: "2026-11-01T04:58:20Z",
		Text: "here is what I see on the phone after the redirect",
		Shots: []desktop.Shot{
			{Name: sentImage, Label: attachImage, Kind: "PNG", Size: "212 KiB", Href: href(sentImage), Image: true},
			{Name: sentLog, Label: attachLog, Kind: "LOG", Size: "1.4 MiB", Href: href(sentLog)},
		},
	}}, tl.Entries...)
	f.timeline[fleetID] = tl
	return f
}

func theme(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}

func chipBox(t *testing.T, tt *ui.Tester, name string) ui.Rect {
	t.Helper()
	r, ok := tt.Find("Attachment " + name)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("no chip for %q in %q", name, tt.Texts())
	}
	return r
}

func assertChipFill(t *testing.T, tt *ui.Tester, name string, want ui.Color) {
	t.Helper()
	r := chipBox(t, tt, name)
	img := tt.Image()
	y := int(r.Y + 2)
	if !rowRun(img, y, want, 50) {
		t.Errorf("the %q chip has no run of 50 px of %v along its top row", name, want)
	}
}

func TestScenesTheComposerWithTwoChips(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			k := colorsFor(dark)
			f := sentFixture(t)
			app := newApp(t, f)
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 11, 41, 0, 0, time.UTC) }
			app.Pick = func() []string { return nil }
			_, img, log := twoFiles(t)
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("when t7 lands, rerun t10 with this log. The redirect still drops the cookie on Safari.")
			app.Attach(img, log)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, attachImage, "PNG · 212 KiB", attachLog, "LOG · 1.4 MiB", "Draft saved 11:41", "Send")
			mustNotText(t, tt, "Uploading")
			a, b := chipBox(t, tt, attachImage), chipBox(t, tt, attachLog)
			box := composerBox(t, tt)
			if a.H != 46 || b.H != 46 {
				t.Errorf("the chips are %.0f and %.0f high, want 46 as the Board draws them", a.H, b.H)
			}
			if a.Y < box.Y || b.Y+b.H > box.Y+box.H {
				t.Errorf("the chips %v %v leave the composer %v", a, b, box)
			}
			if a.Y+a.H > b.Y {
				t.Errorf("the chips overlap: %v then %v", a, b)
			}
			if _, ok := tt.Find("Attach files"); !ok {
				t.Error("the paperclip is not drawn")
			}
			assertChipFill(t, tt, attachImage, k.Panel)
			if got := f.count("upload:" + attachImage); got != 1 {
				t.Errorf("the image uploaded %d times", got)
			}
			shotSized(t, tt, "68-attach-chips-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestScenesAnUploadInFlight(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			f.uploadBlock, f.uploadEntered = make(chan struct{}), make(chan struct{}, 4)
			app, posted := asyncApp(t, f)
			_, img, _ := twoFiles(t)
			tt := sized(t, app, 1100, 760, dark)
			app.Attach(img)
			select {
			case <-f.uploadEntered:
			case <-time.After(5 * time.Second):
				t.Fatal("the upload never started")
			}
			tt.Frame()
			mustText(t, tt, attachImage, "Uploading · 212 KiB", "Attaching 1 file…")
			assertChipFill(t, tt, attachImage, colorsFor(dark).Panel)
			shotSized(t, tt, "69-attach-uploading-1100x760-"+theme(dark), 1100, 760)
			close(f.uploadBlock)
			drainPosts(t, posted)
			tt.Frame()
			mustText(t, tt, "PNG · 212 KiB")
			mustNotText(t, tt, "Attaching 1 file…")
		})
	}
}

func TestSendWaitsForTheUploadsInFlight(t *testing.T) {
	f := sentFixture(t)
	f.uploadBlock, f.uploadEntered = make(chan struct{}), make(chan struct{}, 4)
	app, posted := asyncApp(t, f)
	_, img, log := twoFiles(t)
	app.Draft = "look at these"
	app.Attach(img)
	<-f.uploadEntered
	app.Send()
	drainPosts(t, posted)
	if got := f.count("send:" + fleetID); got != 0 {
		t.Fatalf("Send went out %d times while an upload was in flight", got)
	}
	if app.Draft != "look at these" {
		t.Fatalf("the draft is %q", app.Draft)
	}
	f.uploadBlock <- struct{}{}
	drainPosts(t, posted)
	app.Attach(log)
	<-f.uploadEntered
	app.Send()
	if got := f.count("send:" + fleetID); got != 0 {
		t.Fatal("Send went out while the second upload was in flight")
	}
	f.uploadBlock <- struct{}{}
	drainPosts(t, posted)
	app.Send()
	drainPosts(t, posted)
	f.mu.Lock()
	sent, uploaded := slices.Clone(f.sentFiles), slices.Clone(f.uploaded)
	f.mu.Unlock()
	if len(sent) != 2 || !slices.Equal(sent, uploaded) {
		t.Fatalf("Send passed %v, want the uploaded names %v", sent, uploaded)
	}
}

func TestSendPassesTheChipsInboxNamesAndClearsThemAfterwards(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	_, img, log := twoFiles(t)
	app.Draft = "what is wrong here?"
	app.Attach(img, log)
	app.Send()
	f.mu.Lock()
	sent, text, uploaded := slices.Clone(f.sentFiles), f.sentText, slices.Clone(f.uploaded)
	f.mu.Unlock()
	if text != "what is wrong here?" || len(sent) != 2 || !slices.Equal(sent, uploaded) {
		t.Fatalf("sent %q with %v, want the text and the chips' names %v", text, sent, uploaded)
	}
	if len(app.Chips) != 0 || app.Draft != "" {
		t.Fatalf("after a send %d chips and the draft %q remain", len(app.Chips), app.Draft)
	}
	if len((desktop.Drafts{Dir: app.DraftDir}).LoadFiles(fleetID)) != 0 {
		t.Error("the sent files stayed in the stored draft")
	}
}

func TestAMessageMayBeAttachmentsOnly(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			app := newApp(t, f)
			_, img, _ := twoFiles(t)
			tt := sized(t, app, 1100, 760, dark)
			app.Attach(img)
			tt.Frame()
			shotSized(t, tt, "72-attach-only-1100x760-"+theme(dark), 1100, 760)
			if !rowRun(tt.Image(), sendRow(t, tt), colorsFor(dark).Primary, 50) {
				t.Error("Send is not the primary button with a file and no text")
			}
			click(t, tt, "Send")
			f.mu.Lock()
			sent, text := slices.Clone(f.sentFiles), f.sentText
			f.mu.Unlock()
			if text != "" || len(sent) != 1 || !strings.HasSuffix(sent[0], "-"+attachImage) {
				t.Fatalf("sent %q with %v", text, sent)
			}
			if len(app.Chips) != 0 {
				t.Fatalf("%d chips remain after the send", len(app.Chips))
			}
		})
	}
}

func sendRow(t *testing.T, tt *ui.Tester) int {
	t.Helper()
	r, ok := tt.Find("Send")
	if !ok {
		t.Fatal("no Send button")
	}
	return int(r.Y + 3)
}

func TestSendDoesNothingWithoutTextOrFiles(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	app.Draft = "   \n"
	app.Send()
	if got := f.count("send:" + fleetID); got != 0 {
		t.Fatalf("an empty message went out %d times", got)
	}
}

func TestAFailedSendKeepsTheChips(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	_, img, _ := twoFiles(t)
	app.Draft = "try this"
	app.Attach(img)
	f.action = errors.New("the board did not answer")
	app.Send()
	if len(app.Chips) != 1 || app.Draft != "try this" || app.SendErr == "" {
		t.Fatalf("after a failed send: %d chips, draft %q, error %q", len(app.Chips), app.Draft, app.SendErr)
	}
	f.action = nil
	app.Send()
	f.mu.Lock()
	sent := slices.Clone(f.sentFiles)
	f.mu.Unlock()
	if len(sent) != 1 || len(app.Chips) != 0 {
		t.Fatalf("the retry sent %v and left %d chips", sent, len(app.Chips))
	}
}

func TestScenesAFailedUploadOffersRetry(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			k := colorsFor(dark)
			f := sentFixture(t)
			f.uploadErr = &desktop.APIError{Status: 500, Code: "internal", Message: "the inbox folder could not be written"}
			app := newApp(t, f)
			_, img, _ := twoFiles(t)
			tt := sized(t, app, 1100, 760, dark)
			app.Attach(img)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, attachImage, "the inbox folder could not be written", "Retry")
			assertChipFill(t, tt, attachImage, k.FailBG)
			if got := f.count("upload:" + attachImage); got != 1 {
				t.Fatalf("the upload ran %d times", got)
			}
			shotSized(t, tt, "70-attach-failed-1100x760-"+theme(dark), 1100, 760)
			f.mu.Lock()
			f.uploadErr = nil
			f.mu.Unlock()
			click(t, tt, "Retry "+attachImage)
			tt.Frame()
			mustText(t, tt, "PNG · 212 KiB")
			mustNotText(t, tt, "the inbox folder could not be written")
			if got := f.count("upload:" + attachImage); got != 2 {
				t.Fatalf("Retry ran the upload %d times in all, want 2", got)
			}
		})
	}
}

func TestAFailedUploadNeverSendsAndCanBeDismissed(t *testing.T) {
	f := sentFixture(t)
	f.uploadErr = errors.New("connection refused on 127.0.0.1")
	app := newApp(t, f)
	_, img, _ := twoFiles(t)
	tt := sized(t, app, 1100, 760, false)
	app.Draft = "hello"
	app.Attach(img)
	tt.Frame()
	mustText(t, tt, "The board did not answer; check that hand board is running.")
	mustNotText(t, tt, "127.0.0.1")
	app.Send()
	f.mu.Lock()
	sent := f.sentFiles
	f.mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("a failed upload was sent as %v", sent)
	}
	click(t, tt, "Dismiss "+attachImage)
	tt.Frame()
	if len(app.Chips) != 0 {
		t.Fatalf("%d chips remain after Dismiss", len(app.Chips))
	}
}

func TestAnUploadTheBoardRefusesAsTooLargeOffersNoRetry(t *testing.T) {
	f := sentFixture(t)
	f.uploadErr = &desktop.APIError{Status: 413, Code: "too_large", Message: "a file can be at most 10 MiB"}
	app := newApp(t, f)
	_, img, _ := twoFiles(t)
	tt := sized(t, app, 1100, 760, false)
	app.Attach(img)
	tt.Frame()
	mustText(t, tt, "a file can be at most 10 MiB")
	mustNotText(t, tt, "Retry")
}

func TestScenesAnOverCapFileIsRefused(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			app := newApp(t, f)
			dir := t.TempDir()
			big := sparseOf(t, dir, "core-dump.bin", 11<<20)
			tt := sized(t, app, 1100, 760, dark)
			app.Attach(big)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, "core-dump.bin", "11.0 MiB is over the 10 MiB limit. Paste its path instead.")
			mustNotText(t, tt, "Retry")
			mustNotText(t, tt, dir)
			if got := f.count("upload:core-dump.bin"); got != 0 {
				t.Fatalf("a file over the cap was uploaded %d times", got)
			}
			assertChipFill(t, tt, "core-dump.bin", colorsFor(dark).FailBG)
			shotSized(t, tt, "71-attach-refused-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestAFolderAndAnUnreadableFileAreRefusedWithoutAHostPath(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	dir := t.TempDir()
	folder := filepath.Join(dir, "screens")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	tt := sized(t, app, 1100, 760, false)
	app.Attach(folder, filepath.Join(dir, "gone.txt"))
	tt.Frame()
	mustText(t, tt, "screens/", "A folder is not copied. Paste its path instead.", "gone.txt", "The file could not be read.")
	for _, s := range tt.Texts() {
		if strings.Contains(s, dir) {
			t.Errorf("%q shows a host path", s)
		}
	}
	if n := len(f.uploaded); n != 0 {
		t.Fatalf("%d refused entries uploaded", n)
	}
}

func TestAStaleUploadAnswerAfterAFleetSwitchIsDropped(t *testing.T) {
	f := sentFixture(t)
	f.uploadBlock, f.uploadEntered = make(chan struct{}), make(chan struct{}, 4)
	app, posted := asyncApp(t, f)
	_, img, _ := twoFiles(t)
	app.Attach(img)
	<-f.uploadEntered
	app.SelectFleet(secondID)
	drainPosts(t, posted)
	close(f.uploadBlock)
	drainPosts(t, posted)
	if len(app.Chips) != 0 {
		t.Fatalf("the first fleet's upload landed as %d chips in the second", len(app.Chips))
	}
	for _, id := range []string{fleetID, secondID} {
		if len((desktop.Drafts{Dir: app.DraftDir}).LoadFiles(id)) != 0 {
			t.Errorf("the stale answer was stored in fleet %s's draft", id)
		}
	}
}

func TestRemovingAChipDiscardsItsUploadAnswer(t *testing.T) {
	f := sentFixture(t)
	f.uploadBlock, f.uploadEntered = make(chan struct{}), make(chan struct{}, 4)
	app, posted := asyncApp(t, f)
	_, img, _ := twoFiles(t)
	app.Attach(img)
	<-f.uploadEntered
	app.RemoveChip(1)
	close(f.uploadBlock)
	drainPosts(t, posted)
	if len(app.Chips) != 0 || len((desktop.Drafts{Dir: app.DraftDir}).LoadFiles(fleetID)) != 0 {
		t.Fatalf("a removed chip came back: %d chips", len(app.Chips))
	}
}

func TestADraftKeepsItsChipsAndChecksThemOnRestore(t *testing.T) {
	f := sentFixture(t)
	first := newApp(t, f)
	_, img, log := twoFiles(t)
	first.Attach(img, log)
	if len((desktop.Drafts{Dir: first.DraftDir}).LoadFiles(fleetID)) != 2 {
		t.Fatal("the chips were not stored with the draft")
	}
	again := func() *desktop.App {
		app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
		app.DraftDir = first.DraftDir
		app.Boot()
		return app
	}
	whole := again()
	if len(whole.Chips) != 2 {
		t.Fatalf("the restored draft has %d chips, want 2", len(whole.Chips))
	}
	if got := f.count("head:" + f.uploaded[0]); got != 1 {
		t.Fatalf("the restore checked the first file %d times", got)
	}
	f.mu.Lock()
	delete(f.inbox, f.uploaded[1])
	f.mu.Unlock()
	thinned := again()
	if len(thinned.Chips) != 1 {
		t.Fatalf("with one file gone the draft has %d chips, want 1", len(thinned.Chips))
	}
	if left := (desktop.Drafts{Dir: first.DraftDir}).LoadFiles(fleetID); len(left) != 1 || left[0].Name != f.uploaded[0] {
		t.Fatalf("the stored draft keeps %v", left)
	}
}

func TestScenesADraftRestoredWithOneFileMissing(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			dir := t.TempDir()
			draft := desktop.Drafts{Dir: dir}
			if err := draft.Save(fleetID, "when t7 lands, rerun t10 with this log."); err != nil {
				t.Fatal(err)
			}
			if err := draft.SaveFiles(fleetID, []desktop.InboxFile{
				{Name: sentImage, Label: attachImage, Kind: "image", Size: 212 << 10},
				{Name: "20261031-120000-3-notes.txt", Label: "notes.txt", Kind: "file", Size: 900},
			}); err != nil {
				t.Fatal(err)
			}
			app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
			app.DraftDir = dir
			app.Boot()
			tt := sized(t, app, 1100, 760, dark)
			mustText(t, tt, attachImage, "1 attached file is no longer in inbox/")
			if app.Draft != "when t7 lands, rerun t10 with this log." {
				t.Errorf("the draft text is %q", app.Draft)
			}
			mustNotText(t, tt, "notes.txt")
			if len(app.Chips) != 1 {
				t.Fatalf("%d chips after the restore, want 1", len(app.Chips))
			}
			shotSized(t, tt, "73-attach-restored-1100x760-"+theme(dark), 1100, 760)
			if left := draft.LoadFiles(fleetID); len(left) != 1 {
				t.Errorf("the missing file stayed in the stored draft: %v", left)
			}
		})
	}
}

func TestADraftFilesFileNeverNamesAnotherPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fleetID+".files"),
		[]byte(`[{"name":"../../etc/passwd","label":"x"},{"name":"`+sentImage+`","label":"ok"},{"name":"/abs.png","label":"y"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := (desktop.Drafts{Dir: dir}).LoadFiles(fleetID)
	if len(got) != 1 || got[0].Name != sentImage {
		t.Fatalf("LoadFiles = %+v", got)
	}
	d := desktop.Drafts{Dir: dir}
	if err := d.SaveFiles(fleetID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, fleetID+".files")); !os.IsNotExist(err) {
		t.Fatalf("an empty list left its file behind: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fleetID+".files"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := d.LoadFiles(fleetID); got != nil {
		t.Fatalf("a broken file read as %+v", got)
	}
	if got := d.LoadFiles("not-a-fleet"); got != nil {
		t.Fatalf("a bad fleet id read as %+v", got)
	}
}

func TestThePaperclipOpensThePickerAndAttachesWhatItReturns(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	_, img, log := twoFiles(t)
	calls := 0
	app.Pick = func() []string { calls++; return []string{img, log} }
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Attach files")
	tt.Frame()
	if calls != 1 {
		t.Fatalf("the picker opened %d times", calls)
	}
	mustText(t, tt, attachImage, attachLog)
	app.Pick = func() []string { calls++; return nil }
	click(t, tt, "Attach files")
	tt.Frame()
	if len(app.Chips) != 2 {
		t.Fatalf("a cancelled picker left %d chips", len(app.Chips))
	}
}

func TestAPickerAnsweringAfterAFleetSwitchAttachesNothing(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	_, img, _ := twoFiles(t)
	release := make(chan struct{})
	app.Pick = func() []string { <-release; return []string{img} }
	app.PickFiles()
	app.SelectFleet(secondID)
	close(release)
	drainPosts(t, posted)
	if len(app.Chips) != 0 {
		t.Fatalf("the old fleet's picker attached %d chips to the new one", len(app.Chips))
	}
}

func TestDroppingAFileOnTheChatPaneAttachesIt(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	_, img, _ := twoFiles(t)
	tt := sized(t, app, 1100, 760, false)
	head, ok := tt.Find("Supervisor")
	if !ok {
		t.Fatal("no chat pane")
	}
	if !tt.DropFiles(head.X+40, 300, img) {
		t.Fatal("the chat pane did not take the dropped file")
	}
	tt.Frame()
	mustText(t, tt, attachImage, "PNG · 212 KiB")
	if tt.DropFiles(30, 300, img) {
		t.Error("the work pane took a dropped file")
	}
}

func TestAPastedImageUploadsAsAChip(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	png := f.inbox[sentImage]
	app.AttachBytes(png)
	tt := sized(t, app, 1100, 760, false)
	mustText(t, tt, "image.png")
	f.mu.Lock()
	got := f.inbox[f.uploaded[0]]
	f.mu.Unlock()
	if string(got) != string(png) {
		t.Fatal("the uploaded bytes differ from the pasted image")
	}
	app.AttachBytes(make([]byte, 10<<20+1))
	tt.Frame()
	mustText(t, tt, "10.0 MiB is over the 10 MiB limit. Paste its path instead.")
}

func TestPastingAnImageFromTheClipboardAttachesIt(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	png := f.inbox[sentImage]
	first := true
	tt := ui.NewTester(func(c *ui.Context) {
		if first {
			first = false
			c.Services().WriteClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, png))))
		}
		app.View(c)
	}, 1100, 760)
	click(t, tt, "Message the supervisor")
	paste := ui.Ctrl
	if runtime.GOOS == "darwin" {
		paste = ui.Super
	}
	tt.Key(paste, ui.KeyV)
	tt.Frame()
	mustText(t, tt, "image.png")
	if app.Draft != "" {
		t.Errorf("the paste also typed %q", app.Draft)
	}
}

func TestScenesAMessageWithAnImageAndAFile(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			app := newApp(t, f)
			tt := sized(t, app, 1100, 760, dark)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, "here is what I see on the phone after the redirect", attachLog, "LOG · 1.4 MiB")
			pic, ok := tt.Find("Attached image " + attachImage)
			if !ok || pic.W != 240 || pic.H < 135 || pic.H > 137 {
				t.Fatalf("the sent image is %v, want 240 wide and about 136 high: the Board's 240 by 180 box at the picture's ratio", pic)
			}
			if !hasRun(tt.Image(), ui.Hex("#407acc"), 50) {
				t.Error("the picture's blue stripe is not drawn")
			}
			if got := f.gets(); len(got) != 1 || got[0] != sentImage {
				t.Fatalf("the window fetched %v, want only the image", got)
			}
			shotSized(t, tt, "74-conversation-shots-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestScenesASentImageOpensInTheOverlay(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			app := newApp(t, f)
			tt := sized(t, app, 1100, 760, dark)
			tt.Frame()
			click(t, tt, "Attached image "+attachImage)
			tt.Frame()
			tt.Frame()
			if app.Screen != desktop.DocScreen || app.Ref != "inbox/"+sentImage {
				t.Fatalf("screen %v ref %q, want the overlay on the inbox file", app.Screen, app.Ref)
			}
			mustText(t, tt, "inbox/"+sentImage, "Copy path")
			shotSized(t, tt, "75-conversation-image-overlay-1100x760-"+theme(dark), 1100, 760)
		})
	}
}

func TestASentFileChipOpensInTheOverlay(t *testing.T) {
	f := sentFixture(t)
	f.docs["inbox/"+sentLog] = desktop.Doc{Path: "inbox/" + sentLog, Name: sentLog, Kind: "binary", Size: 1468006, At: at("1")}
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, attachLog)
	tt.Frame()
	tt.Frame()
	if app.Screen != desktop.DocScreen || app.Ref != "inbox/"+sentLog {
		t.Fatalf("screen %v ref %q", app.Screen, app.Ref)
	}
}

func TestAMissingSentFileIsADashedChipThatDoesNotOpen(t *testing.T) {
	f := sentFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries[0].Shots = []desktop.Shot{{Name: "20261101-045800-9-old.txt", Label: "old.txt", Kind: "TXT", Missing: true}}
	f.timeline[fleetID] = tl
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	mustText(t, tt, "old.txt", "no longer in inbox/")
	if got := f.gets(); len(got) != 0 {
		t.Fatalf("a missing file was fetched: %v", got)
	}
	_ = tt.Click("old.txt")
	if app.Screen == desktop.DocScreen {
		t.Fatal("a missing file opened the overlay")
	}
}

func TestAnImageLinePathOutsideTheInboxIsShownAsTextAndNeverFetched(t *testing.T) {
	f := sentFixture(t)
	tl := f.timeline[fleetID]
	tl.Entries[0].Text = "[image: /etc/passwd.png]\nlook"
	tl.Entries[0].Shots = []desktop.Shot{
		{Name: "../../etc/passwd.png", Label: "passwd.png", Kind: "PNG", Image: true, Href: "/x"},
		{Name: "/etc/shadow", Label: "shadow", Kind: "FILE"},
		{Name: "inbox/" + sentImage, Label: "nested.png", Kind: "PNG", Image: true},
	}
	f.timeline[fleetID] = tl
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if got := f.gets(); len(got) != 0 {
		t.Fatalf("the window fetched %v for a name outside inbox/", got)
	}
	if !tt.HasText("[image: /etc/passwd.png]") {
		t.Errorf("the line stayed out of the text: %q", tt.Texts())
	}
	_ = tt.Click("passwd.png")
	if app.Screen == desktop.DocScreen {
		t.Fatal("a name outside inbox/ opened the overlay")
	}
}

func grayPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestADecodeCapHoldsBeforeDecodingASentImage(t *testing.T) {
	f := sentFixture(t)
	f.inbox[sentImage] = grayPNG(t, 6200, 6200)
	if len(f.inbox[sentImage]) > 1<<20 {
		t.Fatal("the fixture is not a small file")
	}
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Attached image " + attachImage); ok {
		t.Fatal("a picture over the pixel cap was drawn")
	}
	mustText(t, tt, attachImage, "PNG · 212 KiB")
	if got := f.count("inbox:" + sentImage); got != 1 {
		t.Fatalf("the image was fetched %d times; a refused picture must not be fetched again", got)
	}
	tt.Frame()
	if got := f.count("inbox:" + sentImage); got != 1 {
		t.Fatalf("a repaint fetched the image again: %d", got)
	}
}

func TestADrawLimitIsCheckedBeforeTheBytesAreDecoded(t *testing.T) {
	header := hugePNG(100, 100)
	if _, why := desktop.DrawBitmap(header, 5000); !strings.Contains(why, "larger than this window reads") {
		t.Fatalf("a picture over the limit says %q", why)
	}
	if _, why := desktop.DrawBitmap(header, 10000); !strings.Contains(why, "could not be drawn") {
		t.Fatalf("a picture within the limit says %q, want it to reach the decoder", why)
	}
}

func TestBytesThatAreNotAPictureShowAsAFileChip(t *testing.T) {
	f := sentFixture(t)
	f.inbox[sentImage] = []byte("not a picture")
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Attached image " + attachImage); ok {
		t.Fatal("bytes that are not a picture were drawn")
	}
	mustText(t, tt, attachImage)
}

func TestAShotAnswerAfterAFleetSwitchIsDropped(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	app.SelectFleet(secondID)
	drainPosts(t, posted)
	tt.Frame()
	if _, ok := tt.Find("Attached image " + attachImage); ok {
		t.Fatal("the first fleet's picture is drawn in the second")
	}
}

func TestScenesTheNarrowWindowKeepsTheChips(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			f := sentFixture(t)
			app := newApp(t, f)
			_, img, log := twoFiles(t)
			tt := sized(t, app, 820, 700, dark)
			click(t, tt, "Chat")
			click(t, tt, "Message the supervisor")
			tt.Type("see the log")
			app.Attach(img, log)
			tt.Frame()
			tt.Frame()
			mustText(t, tt, attachImage, attachLog, "Send")
			box := composerBox(t, tt)
			for _, name := range []string{attachImage, attachLog} {
				if r := chipBox(t, tt, name); r.X < box.X || r.X+r.W > box.X+box.W {
					t.Errorf("the %q chip %v leaves the composer %v", name, r, box)
				}
			}
			if _, ok := tt.Find("Attach files"); !ok {
				t.Error("the paperclip is gone at 820 px")
			}
			shotSized(t, tt, "76-attach-narrow-820x700-"+theme(dark), 820, 700)
		})
	}
}

func TestASendFinishingAfterAFleetSwitchRemovesItsFilesFromTheFirstFleetsDraft(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	_, img, log := twoFiles(t)
	app.Attach(img, log)
	drainPosts(t, posted)
	if len(app.Chips) != 2 {
		t.Fatalf("%d chips", len(app.Chips))
	}
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 2)
	app.Draft = "both"
	app.Send()
	<-f.actionEntered
	app.SelectFleet(secondID)
	close(f.actionBlock)
	drainPosts(t, posted)
	if left := (desktop.Drafts{Dir: app.DraftDir}).LoadFiles(fleetID); len(left) != 0 {
		t.Fatalf("the sent files stayed in the first fleet's draft: %v", left)
	}
	app.SelectFleet(fleetID)
	drainPosts(t, posted)
	if len(app.Chips) != 0 {
		t.Fatalf("coming back restored %d sent files", len(app.Chips))
	}
}

func TestASendFinishingAfterALeaveAndReturnDropsTheRestoredSentChips(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	_, img, _ := twoFiles(t)
	app.Attach(img)
	drainPosts(t, posted)
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 2)
	app.Draft = "one"
	app.Send()
	<-f.actionEntered
	app.SelectFleet(secondID)
	app.SelectFleet(fleetID)
	drainPosts(t, posted)
	if len(app.Chips) != 1 {
		t.Fatalf("the return restored %d chips, want the one being sent", len(app.Chips))
	}
	close(f.actionBlock)
	drainPosts(t, posted)
	if len(app.Chips) != 0 {
		t.Fatalf("%d sent chips remain after the send finished", len(app.Chips))
	}
}

func TestAFailedChipAddedWhileASendRunsSurvivesIt(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	dir, img, _ := twoFiles(t)
	app.Attach(img)
	drainPosts(t, posted)
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 2)
	app.Draft = "one"
	app.Send()
	<-f.actionEntered
	app.Attach(filepath.Join(dir, "gone.txt"))
	close(f.actionBlock)
	drainPosts(t, posted)
	if len(app.Chips) != 1 {
		t.Fatalf("%d chips after the send, want only the failed one added during it", len(app.Chips))
	}
}

func TestAFailedChipRetriedWhileASendRunsIsNotSentAndSurvivesIt(t *testing.T) {
	f := sentFixture(t)
	app, posted := asyncApp(t, f)
	dir, img, _ := twoFiles(t)
	later := filepath.Join(dir, "later.txt")
	app.Attach(img)
	drainPosts(t, posted)
	app.Attach(later)
	if len(app.Chips) != 2 {
		t.Fatalf("%d chips, want the ready one and the failed one", len(app.Chips))
	}
	f.actionBlock, f.actionEntered = make(chan struct{}), make(chan struct{}, 2)
	app.Draft = "one"
	app.Send()
	<-f.actionEntered
	fileOf(t, dir, "later.txt", 10)
	app.RetryChip(2)
	close(f.actionBlock)
	drainPosts(t, posted)
	if len(f.sentFiles) != 1 || strings.Contains(f.sentFiles[0], "later.txt") {
		t.Fatalf("the send carried %v, want only the picture", f.sentFiles)
	}
	if len(app.Chips) != 1 {
		t.Fatalf("%d chips after the send, want the retried one", len(app.Chips))
	}
	if left := (desktop.Drafts{Dir: app.DraftDir}).LoadFiles(fleetID); len(left) != 1 || left[0].Label != "later.txt" {
		t.Fatalf("the draft keeps %v, want later.txt", left)
	}
}

func TestANewerPictureEvictsTheOldestOnceTheBudgetIsSpent(t *testing.T) {
	defer desktop.SetShotBudget(480*270 + 10)()
	f := sentFixture(t)
	older := "20261030-045800-3-older.png"
	f.inbox[older] = f.inbox[sentImage]
	full := f.timeline[fleetID]
	olderEntry := desktop.Entry{Role: "operator", At: "2026-10-31T04:00:00Z", Text: "older",
		Shots: []desktop.Shot{{Name: older, Label: "older.png", Kind: "PNG", Size: "6 KiB", Image: true}}}
	f.timeline[fleetID] = desktop.Timeline{Entries: []desktop.Entry{olderEntry}}
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Attached image older.png"); !ok {
		t.Fatal("the first picture is not drawn")
	}
	full.Entries = append(full.Entries, olderEntry)
	f.timeline[fleetID] = full
	app.Refresh()
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Attached image " + attachImage); !ok {
		t.Error("a newer picture does not draw once the budget is spent")
	}
	if _, ok := tt.Find("Attached image older.png"); ok {
		t.Error("the oldest picture stayed drawn past the budget")
	}
	mustText(t, tt, "older.png", "PNG · 6 KiB")
	if got := f.gets(); len(got) != 2 {
		t.Fatalf("the window fetched %v, want each picture once", got)
	}
}

func TestARestoreAnswerForAnotherFleetLeavesTheSameNamedChipAlone(t *testing.T) {
	f := sentFixture(t)
	blockA, blockB := make(chan struct{}), make(chan struct{})
	f.headBlock = map[string]chan struct{}{fleetID: blockA, secondID: blockB}
	f.headGone = map[string]bool{fleetID: true}
	f.headEntered = make(chan string, 4)
	app, posted := asyncApp(t, f)
	drafts := desktop.Drafts{Dir: app.DraftDir}
	file := desktop.InboxFile{Name: sentImage, Label: attachImage, Kind: "image", Size: 100}
	if err := drafts.SaveFiles(fleetID, []desktop.InboxFile{file}); err != nil {
		t.Fatal(err)
	}
	app.SelectFleet(secondID)
	app.SelectFleet(fleetID)
	if got := <-f.headEntered; got != fleetID {
		t.Fatalf("the first check was for %s", got)
	}
	if err := drafts.SaveFiles(secondID, []desktop.InboxFile{file}); err != nil {
		t.Fatal(err)
	}
	app.SelectFleet(secondID)
	if got := <-f.headEntered; got != secondID {
		t.Fatalf("the second check was for %s", got)
	}
	close(blockA)
	drainPosts(t, posted)
	if len(app.Chips) != 1 {
		t.Fatalf("the first fleet's late answer left %d chips in the second, want its one checking chip", len(app.Chips))
	}
	if left := drafts.LoadFiles(secondID); len(left) != 1 {
		t.Fatalf("the second fleet's draft holds %v", left)
	}
	close(blockB)
	drainPosts(t, posted)
	if len(app.Chips) != 1 {
		t.Fatalf("%d chips after the second fleet's own answer", len(app.Chips))
	}
}

func TestSendLooksDisabledWhileAFileUploads(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(theme(dark), func(t *testing.T) {
			k := colorsFor(dark)
			f := sentFixture(t)
			f.uploadBlock, f.uploadEntered = make(chan struct{}), make(chan struct{}, 4)
			app, posted := asyncApp(t, f)
			_, img, _ := twoFiles(t)
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("see this")
			tt.Frame()
			tt.Frame()
			fill := func() bool {
				r, ok := tt.Find("Send")
				if !ok {
					t.Fatal("no Send button")
				}
				return rgbIs(tt.Image().RGBAAt(int(r.X)+3, int(r.Y+r.H/2)), k.Primary)
			}
			if !fill() {
				t.Fatal("Send is not drawn in its enabled colour with a message typed")
			}
			app.Attach(img)
			<-f.uploadEntered
			tt.Frame()
			tt.Frame()
			if fill() {
				t.Error("Send looks enabled while a file uploads")
			}
			close(f.uploadBlock)
			drainPosts(t, posted)
			tt.Frame()
			tt.Frame()
			if !fill() {
				t.Error("Send stayed dimmed after the upload finished")
			}
		})
	}
}

func TestADraftSaveFailureNamesNoPathOnTheScreen(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked-drafts-folder")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := sentFixture(t)
	app := newApp(t, f)
	app.DraftDir = blocked
	_, img, _ := twoFiles(t)
	tt := sized(t, app, 1100, 760, false)
	click(t, tt, "Message the supervisor")
	app.Attach(img)
	tt.Frame()
	tt.Frame()
	for _, text := range tt.Texts() {
		if strings.Contains(text, "blocked-drafts-folder") || strings.Contains(text, dir) {
			t.Fatalf("a path is on screen after the files draft failed: %q", text)
		}
	}
	mustText(t, tt, "the draft was not saved")
	tt.Type("words")
	tt.Frame()
	tt.Frame()
	for _, text := range tt.Texts() {
		if strings.Contains(text, "blocked-drafts-folder") || strings.Contains(text, dir) {
			t.Fatalf("a path is on screen after the text draft failed: %q", text)
		}
	}
	mustText(t, tt, "the draft was not saved")
	click(t, tt, "second")
	click(t, tt, "golden")
	if strings.Contains(app.DraftErr, dir) {
		t.Fatalf("the restored draft error names a path: %q", app.DraftErr)
	}
}

func TestAConversationLoadsPicturesNewestFirstAndStopsAtItsPixelBudget(t *testing.T) {
	defer desktop.SetShotBudget(480 * 270)()
	f := sentFixture(t)
	older := "20261030-045800-3-older.png"
	f.inbox[older] = f.inbox[sentImage]
	tl := f.timeline[fleetID]
	tl.Entries = append(tl.Entries, desktop.Entry{Role: "operator", At: "2026-10-31T04:00:00Z", Text: "older",
		Shots: []desktop.Shot{{Name: older, Label: "older.png", Kind: "PNG", Size: "6 KiB", Image: true}}})
	f.timeline[fleetID] = tl
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	tt.Frame()
	if got := f.gets(); len(got) != 1 || got[0] != sentImage {
		t.Fatalf("the window fetched %v, want only the newest picture once the budget is spent", got)
	}
	if _, ok := tt.Find("Attached image " + attachImage); !ok {
		t.Error("the newest picture is not drawn")
	}
	if _, ok := tt.Find("Attached image older.png"); ok {
		t.Error("a picture past the budget was drawn")
	}
	mustText(t, tt, "older.png", "PNG · 6 KiB")
}

func TestManyChipsScrollInsideABoundedRowAndKeepSendInView(t *testing.T) {
	f := sentFixture(t)
	app := newApp(t, f)
	dir := t.TempDir()
	tt := sized(t, app, 820, 600, false)
	click(t, tt, "Chat")
	for i := range 40 {
		app.Attach(fileOf(t, dir, "file-"+strings.Repeat("x", 30)+string(rune('a'+i%26))+string(rune('a'+i/26))+".log", 100))
	}
	tt.Frame()
	tt.Frame()
	box := composerBox(t, tt)
	send, ok := tt.Find("Send")
	if !ok || send.Y+send.H > 600 || box.Y < 0 {
		t.Fatalf("Send %v and the composer %v do not fit a 600 px window", send, box)
	}
	if box.H > 90+kit.ChipRowMax+40 {
		t.Fatalf("40 chips made the composer %.0f high; the chip row is bounded at %d", box.H, kit.ChipRowMax)
	}
}
