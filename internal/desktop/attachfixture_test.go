package desktop_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/desktop"
)

func (f *fixture) UploadInbox(ctx context.Context, id, name string, body []byte) (desktop.InboxFile, error) {
	f.note("upload:" + name)
	if f.uploadEntered != nil {
		f.uploadEntered <- struct{}{}
	}
	if f.uploadBlock != nil {
		<-f.uploadBlock
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadErr != nil {
		return desktop.InboxFile{}, f.uploadErr
	}
	if f.inbox == nil {
		f.inbox = map[string][]byte{}
	}
	stored := fmt.Sprintf("20261101-050000-%d-%s", len(f.uploaded)+1, name)
	f.inbox[stored] = body
	f.uploaded = append(f.uploaded, stored)
	kind := "file"
	if strings.HasSuffix(name, ".png") {
		kind = "image"
	}
	return desktop.InboxFile{Name: stored, Label: name, Kind: kind, Size: int64(len(body))}, nil
}

func (f *fixture) InboxHead(ctx context.Context, id, name string) (int64, error) {
	f.note("head:" + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.inbox[name]
	if !ok {
		return 0, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: file in inbox/"}
	}
	return int64(len(b)), nil
}

func (f *fixture) InboxBytes(ctx context.Context, id, name string) ([]byte, error) {
	f.note("inbox:" + name)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inboxGets = append(f.inboxGets, name)
	b, ok := f.inbox[name]
	if !ok {
		return nil, &desktop.APIError{Status: 404, Code: "not_found", Message: "not found: file in inbox/"}
	}
	return b, nil
}

func (f *fixture) gets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.inboxGets...)
}
