package desktop_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func (f *fixture) HistorySearch(ctx context.Context, id, q string, limit int) (desktop.HistorySearch, error) {
	return desktop.HistorySearch{}, errors.New("no history in this fixture")
}

func (f *fixture) HistoryEntry(ctx context.Context, id, ref string) (desktop.HistoryEntry, error) {
	return desktop.HistoryEntry{}, errors.New("no history in this fixture")
}

type histCall struct {
	Q     string
	Limit int
}

type histModel struct {
	desktop.ReadModel
	mu       sync.Mutex
	pages    map[int]desktop.Timeline
	befores  []int
	hits     map[string]desktop.HistorySearch
	entries  map[string]desktop.HistoryEntry
	features []string
	searches []histCall

	timelineErr, olderErr, searchErr, entryErr error

	gate    map[string]chan struct{}
	entered chan string
}

func (m *histModel) Features(ctx context.Context, id string) ([]string, error) {
	return m.features, nil
}

func (m *histModel) Timeline(ctx context.Context, id string, before int) (desktop.Timeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.befores = append(m.befores, before)
	if before == 0 && m.timelineErr != nil {
		return desktop.Timeline{}, m.timelineErr
	}
	if before > 0 && m.olderErr != nil {
		return desktop.Timeline{}, m.olderErr
	}
	return m.pages[before], nil
}

func (m *histModel) HistorySearch(ctx context.Context, id, q string, limit int) (desktop.HistorySearch, error) {
	m.mu.Lock()
	m.searches = append(m.searches, histCall{q, limit})
	gate, entered, err, out := m.gate[q], m.entered, m.searchErr, m.hits[q]
	m.mu.Unlock()
	if gate != nil {
		entered <- q
		<-gate
	}
	if err != nil {
		return desktop.HistorySearch{}, err
	}
	return out, nil
}

func (m *histModel) HistoryEntry(ctx context.Context, id, ref string) (desktop.HistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entryErr != nil {
		return desktop.HistoryEntry{}, m.entryErr
	}
	e, ok := m.entries[ref]
	if !ok {
		return desktop.HistoryEntry{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "no such entry"}
	}
	return e, nil
}

func (m *histModel) olders() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []int
	for _, b := range m.befores {
		if b > 0 {
			out = append(out, b)
		}
	}
	return out
}

func newHistModel(f *fixture) *histModel {
	op := func(n int64, at, text string) desktop.Entry {
		return desktop.Entry{Role: "operator", At: at, Text: text, Input: n, Ref: "s3", Delivery: "delivered"}
	}
	sup := func(at, text string) desktop.Entry {
		return desktop.Entry{Role: "supervisor", At: at, Text: text, Ref: "s3", Model: "sonnet"}
	}
	hit := func(ref, who, at, text string) desktop.HistoryHit {
		return desktop.HistoryHit{Ref: ref, Who: who, At: at, Text: text}
	}
	return &histModel{
		ReadModel: f,
		features:  []string{"fleets", "history"},
		pages: map[int]desktop.Timeline{
			0: {Live: true, Older: 40, Entries: []desktop.Entry{
				sup("2026-11-24T10:05:00Z", "Build is green. Shipping the amber fix."),
				op(12, "2026-11-24T10:04:00Z", "ship it"),
			}},
			40: {Older: 20, Entries: []desktop.Entry{
				sup("2026-11-24T09:01:00Z", "The first reply at nine."),
				sup("2026-11-24T09:01:00Z", "The second reply at nine, about the kiln."),
				op(7, "2026-11-24T09:00:00Z", "what about the kiln schedule?"),
			}},
			20: {Older: 0, Entries: []desktop.Entry{
				op(3, "2026-11-23T07:00:00Z", "start the work"),
			}},
		},
		hits: map[string]desktop.HistorySearch{
			"kiln": {Q: "kiln", Total: 4, More: true, Hits: []desktop.HistoryHit{
				hit("i7", "operator", "2026-11-24T09:00:00Z", "what about the «kiln» schedule?"),
				hit("y2", "supervisor", "2026-11-24T09:01:00Z", "The second reply at nine, about the «kiln»."),
				hit("r5", "report", "2026-11-24T08:30:00Z", "…the «kiln» build passed…"),
			}},
			"amber": {Q: "amber", Total: 1, Hits: []desktop.HistoryHit{
				hit("y9", "supervisor", "2026-11-24T10:05:00Z", "Build is green. Shipping the «amber» fix."),
			}},
			"ghost": {Q: "ghost", Hits: []desktop.HistoryHit{}},
			"gone": {Q: "gone", Total: 1, Hits: []desktop.HistoryHit{
				hit("i99", "operator", "2026-01-01T00:00:00Z", "a «gone» message"),
			}},
		},
		entries: map[string]desktop.HistoryEntry{
			"i7":  {Ref: "i7", Who: "operator", At: "2026-11-24T09:00:00Z", Text: "what about the kiln schedule?"},
			"y2":  {Ref: "y2", Who: "supervisor", At: "2026-11-24T09:01:00Z", Supervisor: "s3", Text: "The second reply at nine, about the kiln."},
			"y9":  {Ref: "y9", Who: "supervisor", At: "2026-11-24T10:05:00Z", Supervisor: "s3", Text: "Build is green. Shipping the amber fix."},
			"r5":  {Ref: "r5", Who: "report", At: "2026-11-24T08:30:00Z", Task: "t4", Text: "The kiln build passed.", Truncated: true},
			"i99": {Ref: "i99", Who: "operator", At: "2026-01-01T00:00:00Z", Text: "a gone message"},
		},
	}
}

func historyApp(t *testing.T, m *histModel) *desktop.App {
	t.Helper()
	inUTC(t)
	app := newDesktop(func() (desktop.ReadModel, error) { return m, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	app.Show(desktop.HistoryScreen)
	return app
}

func historyModel(t *testing.T) *histModel { return newHistModel(goldenFixture(t)) }
