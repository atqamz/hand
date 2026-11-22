package desktop_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/sidecar"
)

func themed(t *testing.T, name string, scene func(t *testing.T, dark bool, name string)) {
	t.Helper()
	for _, dark := range []bool{false, true} {
		suffix := "-light"
		if dark {
			suffix = "-dark"
		}
		scene(t, dark, name+suffix)
	}
}

func TestScenesTheConnectFormAndItsConfirmation(t *testing.T) {
	themed(t, "remote-form", func(t *testing.T, dark bool, name string) {
		r := newRemoteUI(t, false)
		r.tt.SetDark(dark)
		openForm(t, r)
		r.app.RailOpen = true
		r.tt.Frame()
		mustText(t, r.tt, "Connect to a remote fleet.", "Continue", "Cancel")
		shot(t, r.tt, name)
		typeInto(t, r.tt, "Pairing link", "handboard://c#v=1&e="+rtHostB+"&f="+rtB+"&c="+pairCode+"&n=Lab")
		click(t, r.tt, "Continue")
		r.tt.Frame()
		mustText(t, r.tt, "Add bbbb…bbbb?")
		shot(t, r.tt, strings.Replace(name, "form", "confirm", 1))
	})
}

func TestScenesEachConnectionStateInTheRailAndTheHead(t *testing.T) {
	states := map[string]func(*remoteUI) sidecar.ClientStatus{
		"connected":   func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Connected) },
		"connecting":  func(r *remoteUI) sidecar.ClientStatus { return up(r, "") },
		"busy":        func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Busy) },
		"unreachable": func(r *remoteUI) sidecar.ClientStatus { return up(r, sidecar.Unreachable) },
	}
	for state, st := range states {
		themed(t, "remote-indicator-"+state, func(t *testing.T, dark bool, name string) {
			r := newRemoteUI(t, true)
			r.tt.SetDark(dark)
			r.app.RailOpen = true
			r.status(st(r))
			r.tt.Frame()
			shot(t, r.tt, name)
		})
	}
	themed(t, "remote-indicator-board-not-running", func(t *testing.T, dark bool, name string) {
		r := newRemoteUI(t, true)
		r.tt.SetDark(dark)
		r.app.RailOpen = true
		r.status(up(r, sidecar.BoardDown))
		r.host.fail = func(http.ResponseWriter) bool { panic(http.ErrAbortHandler) }
		r.app.Refresh()
		r.tt.Frame()
		mustText(t, r.tt, "Board not running on the host. Start it there.")
		shot(t, r.tt, name)
	})
}

func TestScenesRevokedAndReplacedKey(t *testing.T) {
	themed(t, "remote-revoked", func(t *testing.T, dark bool, name string) {
		r := newRemoteUI(t, true)
		r.tt.SetDark(dark)
		r.app.RailOpen = true
		r.host.fail = func(w http.ResponseWriter) bool {
			w.Header().Set("X-Hand-Error", "no longer paired")
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		r.app.Refresh()
		r.tt.Frame()
		mustText(t, r.tt, "Revoked on the host.")
		shot(t, r.tt, name)
	})
	themed(t, "remote-key-replaced", func(t *testing.T, dark bool, name string) {
		r := newRemoteUI(t, true)
		r.tt.SetDark(dark)
		st := up(r, sidecar.Connected)
		st.ID = rtOther
		r.state(t, st)
		mustText(t, r.tt, "Key replaced")
		shot(t, r.tt, name)
	})
}

func TestScenesARemoteFleetWithItsLocalOnlyButtonsHidden(t *testing.T) {
	themed(t, "remote-hidden-buttons", func(t *testing.T, dark bool, name string) {
		app, tt := remoteGolden(t)
		tt.SetDark(dark)
		app.RailOpen = true
		tt.Frame()
		mustText(t, tt, "Connected")
		shot(t, tt, name)
	})
}
