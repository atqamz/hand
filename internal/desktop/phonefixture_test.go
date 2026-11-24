package desktop_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

const (
	phoneLink = "https://host.example/f00000000e0aa/pair#K7QM3-XRD2P"
	phoneCode = "K7QM3-XRD2P"
	appLink   = "hand://pair/ABCDEFGH0123456789"
)

func (f *fixture) Phone(ctx context.Context, id string) (desktop.Phone, error) {
	return desktop.Phone{}, errors.New("no phone in this fixture")
}

func (f *fixture) PhoneCode(ctx context.Context, id string, req desktop.PhoneCodeRequest) (desktop.Phone, error) {
	return desktop.Phone{}, errors.New("no phone in this fixture")
}

func (f *fixture) PhoneApp(ctx context.Context, id string) (desktop.Phone, error) {
	return desktop.Phone{}, errors.New("no phone in this fixture")
}

func (f *fixture) PhoneRevoke(ctx context.Context, id string, req desktop.PhoneRevokeRequest) (desktop.Phone, error) {
	return desktop.Phone{}, errors.New("no phone in this fixture")
}

type phoneModel struct {
	desktop.ReadModel
	mu       sync.Mutex
	data     desktop.Phone
	features []string
	clock    *time.Time

	readErr, codeErr, appErr, revokeErr error
	revokeOut                           *desktop.Phone
	reads                               int
	codeReqs                            []desktop.PhoneCodeRequest
	revokes                             []desktop.PhoneRevokeRequest
	apps                                int
	plain                               bool
	link                                string

	gate    chan struct{}
	entered chan struct{}

	revokeGate    chan struct{}
	revokeEntered chan struct{}
}

func (m *phoneModel) Features(ctx context.Context, id string) ([]string, error) {
	return m.features, nil
}

func (m *phoneModel) Phone(ctx context.Context, id string) (desktop.Phone, error) {
	if m.entered != nil {
		m.entered <- struct{}{}
		<-m.gate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	if m.readErr != nil {
		return desktop.Phone{}, m.readErr
	}
	return m.data, nil
}

func (m *phoneModel) expiry() string {
	return m.clock.Add(5 * time.Minute).UTC().Format(time.RFC3339)
}

func (m *phoneModel) PhoneCode(ctx context.Context, id string, req desktop.PhoneCodeRequest) (desktop.Phone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codeReqs = append(m.codeReqs, req)
	if m.codeErr != nil {
		return desktop.Phone{}, m.codeErr
	}
	fleets := []desktop.PhoneFleetRef{{ID: fleetID, Name: "golden"}}
	for _, f := range req.Fleets {
		if f == secondID {
			fleets = append(fleets, desktop.PhoneFleetRef{ID: secondID, Name: "kiln"})
		}
	}
	d := m.data
	link := phoneLink
	if m.link != "" {
		link = m.link
	}
	d.Code = &desktop.PhoneCode{Code: phoneCode, Link: desktop.Secret(link), URL: req.Base + "/" + fleetID + "/pair", Address: req.Base, PlainHTTP: m.plain, Fleets: fleets, ExpiresAt: m.expiry()}
	m.data = d
	return d, nil
}

func (m *phoneModel) PhoneApp(ctx context.Context, id string) (desktop.Phone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.apps++
	if m.appErr != nil {
		return desktop.Phone{}, m.appErr
	}
	d := m.data
	d.App = &desktop.PhoneApp{Link: appLink, ExpiresAt: m.expiry()}
	m.data = d
	return d, nil
}

func (m *phoneModel) PhoneRevoke(ctx context.Context, id string, req desktop.PhoneRevokeRequest) (desktop.Phone, error) {
	if m.revokeEntered != nil {
		m.revokeEntered <- struct{}{}
		<-m.revokeGate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokes = append(m.revokes, req)
	if m.revokeErr != nil {
		return desktop.Phone{}, m.revokeErr
	}
	if m.revokeOut != nil {
		m.data = *m.revokeOut
		return m.data, nil
	}
	d := m.data
	d.Devices = slices.DeleteFunc(slices.Clone(d.Devices), func(x desktop.PhoneDevice) bool {
		return req.ID != 0 && x.ID == req.ID || req.Phone != "" && x.Phone == req.Phone
	})
	m.data = d
	return d, nil
}

func phoneDevices() []desktop.PhoneDevice {
	return []desktop.PhoneDevice{
		{Phone: "ph-pixel", Name: "Pixel 8", Login: "Chrome", PairedAt: "2026-11-20T08:00:00Z", SeenAt: "2026-11-24T09:41:00Z", Fleets: []desktop.PhoneAt{
			{ID: fleetID, Name: "golden", Current: true},
			{ID: secondID, Name: "kiln"},
		}},
		{ID: 7, Name: "Old tablet", PairedAt: "2026-08-02T08:00:00Z", Expired: true},
	}
}

func newPhoneModel(f *fixture) *phoneModel {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	return &phoneModel{
		ReadModel: f,
		features:  []string{"fleets", "phone"},
		clock:     &now,
		data: desktop.Phone{
			Iroh: &desktop.PhoneIroh{State: "ready", Tone: "pass", Text: "Companion access over iroh is online. The Companion can pair with this host.", ID: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			Fleets: []desktop.PhoneFleet{
				{ID: fleetID, Name: "golden", Current: true, Selected: true},
				{ID: secondID, Name: "kiln"},
			},
			Devices: phoneDevices(),
		},
	}
}

func phoneFixture(t *testing.T) *phoneModel { return newPhoneModel(goldenFixture(t)) }

func phoneApp(t *testing.T, m *phoneModel) *desktop.App {
	t.Helper()
	inUTC(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return m, nil })
	app.DraftDir = t.TempDir()
	app.Now = func() time.Time { return *m.clock }
	app.Boot()
	app.Show(desktop.PhoneScreen)
	return app
}
