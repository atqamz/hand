package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

const (
	uploadMax = 10 << 20
	pasteName = "image.png"
)

var (
	inboxName = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9]+(\.(png|jpg|webp|gif)|-[A-Za-z0-9_][A-Za-z0-9._-]{0,79})$`)
	kindExt   = regexp.MustCompile(`\.([^.]{1,4})$`)

	errUnreadable = &refusal{"The file could not be read.", true}
)

const copyFolder = "A folder is not copied. Paste its path instead."

type refusal struct {
	msg   string
	retry bool
}

func (r *refusal) Error() string { return r.msg }

type chipState int

const (
	chipReady chipState = iota
	chipUploading
	chipChecking
	chipFailed
)

type chip struct {
	id     int
	state  chipState
	file   InboxFile
	title  string
	size   int64
	err    string
	retry  bool
	src    string
	data   []byte
	cancel context.CancelFunc
}

func chipKind(title string) string {
	if strings.HasSuffix(title, "/") {
		return "DIR"
	}
	if m := kindExt.FindStringSubmatch(title); m != nil {
		return strings.ToUpper(m[1])
	}
	return "FILE"
}

func (a *App) offload(work func() func()) {
	if a.Post == nil {
		work()()
		return
	}
	post := a.Post
	go func() { post(work()) }()
}

func (a *App) chipAt(id int) int {
	for i := range a.Chips {
		if a.Chips[i].id == id {
			return i
		}
	}
	return -1
}

func (a *App) attaching() int {
	n := 0
	for _, ch := range a.Chips {
		if ch.state == chipUploading || ch.state == chipChecking {
			n++
		}
	}
	return n
}

func (a *App) readyChips() (ids []int, names []string) {
	for _, ch := range a.Chips {
		if ch.state == chipReady {
			ids, names = append(ids, ch.id), append(names, ch.file.Name)
		}
	}
	return ids, names
}

func (a *App) addChip(ch chip) int {
	a.chipSeq++
	ch.id = a.chipSeq
	a.Chips = append(a.Chips, ch)
	return ch.id
}

func (a *App) failChip(title string, r *refusal, src string) {
	a.addChip(chip{state: chipFailed, title: title, err: r.msg, retry: r.retry, src: src})
	a.attachSay = title + " was not attached: " + r.msg
}

func (a *App) canAttach() bool { return a.Model != nil && a.Fleet.ID != "" }

func (a *App) Attach(paths ...string) {
	if !a.canAttach() {
		return
	}
	for _, path := range paths {
		title := filepath.Base(path)
		fi, err := os.Stat(path)
		switch {
		case err != nil || !fi.IsDir() && !fi.Mode().IsRegular():
			a.failChip(shownText(title), errUnreadable, path)
		case fi.IsDir():
			a.failChip(shownText(title)+"/", &refusal{copyFolder, false}, "")
		case fi.Size() > uploadMax:
			a.failChip(shownText(title), overCap(fi.Size()), "")
		default:
			a.upload(a.addChip(chip{state: chipUploading, title: shownText(title), size: fi.Size(), src: path}))
		}
	}
}

func overCap(size int64) *refusal {
	return &refusal{docSize(size) + " is over the 10 MiB limit. Paste its path instead.", false}
}

func (a *App) AttachBytes(data []byte) {
	if !a.canAttach() {
		return
	}
	if len(data) > uploadMax {
		a.failChip(pasteName, overCap(int64(len(data))), "")
		return
	}
	a.upload(a.addChip(chip{state: chipUploading, title: pasteName, size: int64(len(data)), data: data}))
}

func readUpload(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errUnreadable
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, uploadMax+1))
	switch {
	case err != nil:
		return nil, errUnreadable
	case len(b) > uploadMax:
		return nil, overCap(int64(len(b)))
	}
	return b, nil
}

func uploadFailure(err error) (string, bool) {
	var r *refusal
	var unread *AppliedUnread
	var api *APIError
	switch {
	case errors.As(err, &r):
		return r.msg, r.retry
	case errors.As(err, &unread):
		return "The file could not be attached; try again.", true
	case errors.As(err, &api):
		return friendly(err), api.Status != http.StatusRequestEntityTooLarge
	}
	return "The board did not answer; check that hand board is running.", true
}

func (a *App) upload(id int) {
	i := a.chipAt(id)
	if i < 0 {
		return
	}
	ctx, cancel := context.WithCancel(a.Context)
	ch := &a.Chips[i]
	ch.state, ch.err, ch.retry, ch.cancel = chipUploading, "", false, cancel
	src, data, title, model, fleet, gen := ch.src, ch.data, ch.title, a.Model, a.Fleet.ID, a.chipGen
	a.offload(func() func() {
		var f InboxFile
		body, err := data, error(nil)
		if body == nil {
			body, err = readUpload(src)
		}
		if err == nil {
			f, err = model.UploadInbox(ctx, fleet, title, body)
		}
		cancel()
		return func() { a.uploaded(gen, fleet, id, f, err) }
	})
}

func (a *App) uploaded(gen int, fleet string, id int, f InboxFile, err error) {
	if gen != a.chipGen || fleet != a.Fleet.ID {
		return
	}
	i := a.chipAt(id)
	if i < 0 {
		return
	}
	ch := &a.Chips[i]
	ch.cancel = nil
	if err != nil {
		ch.state = chipFailed
		ch.err, ch.retry = uploadFailure(err)
		a.attachSay = ch.title + " was not attached: " + ch.err
		return
	}
	ch.state, ch.file, ch.data = chipReady, f, nil
	a.attachSay = "Attached " + shownText(f.Label)
	a.chipsChanged()
}

func (a *App) RetryChip(id int) {
	if i := a.chipAt(id); i >= 0 && a.Chips[i].state == chipFailed && a.Chips[i].retry {
		a.upload(id)
	}
}

func (a *App) RemoveChip(id int) {
	i := a.chipAt(id)
	if i < 0 {
		return
	}
	if a.Chips[i].cancel != nil {
		a.Chips[i].cancel()
	}
	a.Chips = append(a.Chips[:i], a.Chips[i+1:]...)
	a.chipsChanged()
}

func (a *App) dropChips() {
	for _, ch := range a.Chips {
		if ch.cancel != nil {
			ch.cancel()
		}
	}
	if a.chipStop != nil {
		a.chipStop()
		a.chipStop = nil
	}
	a.Chips, a.attachNote, a.attachSay = nil, "", ""
	a.chipGen++
}

func (a *App) chipsChanged() {
	a.attachNote = ""
	a.draftAt, a.draftRestored = a.now(), false
	var files []InboxFile
	for _, ch := range a.Chips {
		if ch.state == chipReady || ch.state == chipChecking {
			files = append(files, ch.file)
		}
	}
	if err := a.drafts().SaveFiles(a.Fleet.ID, files); err != nil {
		a.DraftErr = "the draft was not saved: " + friendly(err)
	}
}

func (a *App) settledChips() []int {
	var ids []int
	for _, ch := range a.Chips {
		if ch.state == chipReady || ch.state == chipFailed {
			ids = append(ids, ch.id)
		}
	}
	return ids
}

func (a *App) sentChips(gen int, fleet string, ids []int, names []string) {
	if a.Fleet.ID == fleet && a.chipGen == gen {
		a.Chips = slices.DeleteFunc(a.Chips, func(ch chip) bool { return slices.Contains(ids, ch.id) })
		a.chipsChanged()
		return
	}
	drafts := a.drafts()
	if err := drafts.SaveFiles(fleet, slices.DeleteFunc(drafts.LoadFiles(fleet), func(f InboxFile) bool { return slices.Contains(names, f.Name) })); err != nil {
		a.DraftErr = "the draft was not saved: " + friendly(err)
	}
	if a.Fleet.ID == fleet {
		a.Chips = slices.DeleteFunc(a.Chips, func(ch chip) bool { return ch.state != chipFailed && slices.Contains(names, ch.file.Name) })
		a.chipsChanged()
	}
}

func (a *App) PickFiles() {
	if a.Pick == nil || !a.canAttach() {
		return
	}
	gen, fleet, pick := a.chipGen, a.Fleet.ID, a.Pick
	a.offload(func() func() {
		paths := pick()
		return func() {
			if gen == a.chipGen && fleet == a.Fleet.ID {
				a.Attach(paths...)
			}
		}
	})
}

func (a *App) restoreChips() {
	files := a.drafts().LoadFiles(a.Fleet.ID)
	if len(files) == 0 || a.Model == nil {
		return
	}
	for _, f := range files {
		a.addChip(chip{state: chipChecking, file: f, title: shownText(f.Label), size: f.Size})
	}
	gen, fleet, model := a.chipGen, a.Fleet.ID, a.Model
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	a.chipStop = cancel
	a.offload(func() func() {
		defer cancel()
		sizes, gone := make([]int64, len(files)), make([]bool, len(files))
		for i, f := range files {
			n, err := model.InboxHead(ctx, fleet, f.Name)
			var api *APIError
			gone[i] = errors.As(err, &api) && api.Status == 404
			sizes[i] = n
		}
		return func() { a.restored(gen, fleet, files, sizes, gone) }
	})
}

func (a *App) restored(gen int, fleet string, files []InboxFile, sizes []int64, gone []bool) {
	if gen != a.chipGen || fleet != a.Fleet.ID {
		return
	}
	var missing []string
	for i, f := range files {
		at := -1
		for j := range a.Chips {
			if a.Chips[j].state == chipChecking && a.Chips[j].file.Name == f.Name {
				at = j
				break
			}
		}
		if at < 0 {
			continue
		}
		if gone[i] {
			missing = append(missing, shownText(f.Label))
			a.Chips = append(a.Chips[:at], a.Chips[at+1:]...)
			continue
		}
		if sizes[i] > 0 {
			a.Chips[at].file.Size = sizes[i]
		}
		a.Chips[at].state = chipReady
	}
	if len(missing) == 0 {
		a.draftRestored = true
		return
	}
	a.chipsChanged()
	a.attachSay = strings.Join(missing, ", ") + " is no longer in inbox/"
	a.attachNote = "1 attached file is no longer in inbox/"
	if len(missing) > 1 {
		a.attachSay = strings.Join(missing, ", ") + " are no longer in inbox/"
		a.attachNote = fmt.Sprintf("%d attached files are no longer in inbox/", len(missing))
	}
}

func (a *App) attachingNote() string {
	switch n := a.attaching(); n {
	case 0:
		return ""
	case 1:
		return "Attaching 1 file…"
	default:
		return fmt.Sprintf("Attaching %d files…", n)
	}
}

func (a *App) chipMeta(ch chip) string {
	switch ch.state {
	case chipUploading:
		return "Uploading · " + docSize(ch.size)
	case chipChecking:
		return "Checking…"
	case chipFailed:
		return ch.err
	}
	size := ch.file.Size
	if size == 0 {
		size = ch.size
	}
	return chipKind(ch.title) + " · " + docSize(size)
}

func (a *App) chipTitle(ch chip) string {
	if ch.state == chipReady && ch.file.Label != "" {
		return shownText(ch.file.Label)
	}
	return ch.title
}

func (a *App) pasteImage(clip ui.Services) bool {
	if !slices.Contains(clip.ClipboardFormats(), transfer.PNG) {
		return false
	}
	b, err := clip.ReadClipboardData(transfer.PNG).Read(transfer.PNG)
	if err != nil || len(b) == 0 {
		return false
	}
	a.AttachBytes(b)
	return true
}
