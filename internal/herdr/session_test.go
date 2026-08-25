package herdr

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func sessionListResponse(sessions string) faketool.HerdrResponse {
	return faketool.HerdrResponse{
		Command: "session list",
		Stdout:  `{"sessions":[` + sessions + `]}`,
	}
}

func serverStatusResponse(status string, running, compatible bool, session string) faketool.HerdrResponse {
	return faketool.HerdrResponse{
		Command: "status server",
		Stdout: `{"status":"` + status + `","running":` + boolString(running) +
			`,"version":"0.8.2","protocol":20,"compatible":` + boolString(compatible) +
			`,"session":"` + session + `"}`,
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func observeTestSession(t *testing.T) SessionObservation {
	t.Helper()
	return ObserveFleetSession(context.Background(), "f_fleet")
}

func TestObserveFleetSessionClassifiesExactSessionStates(t *testing.T) {
	tests := []struct {
		name      string
		session   string
		status    *faketool.HerdrResponse
		wantState SessionState
	}{
		{
			name:      "running compatible exact session",
			session:   `{"default":false,"name":"hand-f_fleet","running":true,"socket_path":"/stale/socket"}`,
			status:    ptr(serverStatusResponse("running", true, true, "hand-f_fleet")),
			wantState: SessionRunningCompatible,
		},
		{
			name:      "default and another Fleet do not satisfy target",
			session:   `{"default":true,"name":"default","running":true},{"default":false,"name":"hand-f_other","running":true},{"default":false,"name":"hand-f_fleet","running":false,"socket_path":"/stale/socket"}`,
			wantState: SessionStopped,
		},
		{
			name:      "incompatible runtime",
			session:   `{"default":false,"name":"hand-f_fleet","running":true}`,
			status:    ptr(serverStatusResponse("running", true, false, "hand-f_fleet")),
			wantState: SessionIncompatible,
		},
		{
			name:    "provider confirms server stopped",
			session: `{"default":false,"name":"hand-f_fleet","running":true}`,
			status: ptr(faketool.HerdrResponse{
				Command: "status server",
				Stdout:  `{"error":{"code":"server_not_running","message":"not running"}}`,
				Exit:    1,
			}),
			wantState: SessionStopped,
		},
		{
			name:      "missing exact session",
			session:   `{"default":true,"name":"default","running":true}`,
			wantState: SessionStopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := []faketool.HerdrResponse{sessionListResponse(tt.session)}
			if tt.status != nil {
				responses = append(responses, *tt.status)
			}
			bin := faketool.Bin(t)
			faketool.Herdr{Responses: responses}.Install(t, bin)

			got := observeTestSession(t)
			if got.Name != "hand-f_fleet" || got.State != tt.wantState {
				t.Fatalf("observation = %+v, want name hand-f_fleet and state %q", got, tt.wantState)
			}
			if got.Reason == "" {
				t.Fatalf("observation = %+v, want a bounded reason", got)
			}
		})
	}
}

func TestObserveFleetSessionProviderFailuresAreUnknown(t *testing.T) {
	tests := []struct {
		name     string
		response faketool.HerdrResponse
	}{
		{
			name:     "query failure",
			response: faketool.HerdrResponse{Command: "session list", Stderr: "permission denied", Exit: 1},
		},
		{
			name:     "malformed list",
			response: faketool.HerdrResponse{Command: "session list", Stdout: `{"sessions":`, Exit: 0},
		},
		{
			name:     "malformed status",
			response: sessionListResponse(`{"default":false,"name":"hand-f_fleet","running":true}`),
		},
		{
			name:     "status omits running state",
			response: sessionListResponse(`{"default":false,"name":"hand-f_fleet","running":true}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := []faketool.HerdrResponse{tt.response}
			if tt.name == "malformed status" {
				responses = append(responses, faketool.HerdrResponse{Command: "status server", Stdout: `{`, Exit: 0})
			}
			if tt.name == "status omits running state" {
				responses = append(responses, faketool.HerdrResponse{Command: "status server", Stdout: `{"status":"running","compatible":true}`, Exit: 0})
			}
			bin := faketool.Bin(t)
			faketool.Herdr{Responses: responses}.Install(t, bin)

			got := observeTestSession(t)
			if got.State != SessionUnknown {
				t.Fatalf("observation = %+v, want unknown", got)
			}
			if got.Reason == "" || strings.Contains(got.Reason, "{") {
				t.Fatalf("observation = %+v, want bounded reason", got)
			}
		})
	}
}

func TestObserveFleetSessionDoesNotTrustAStaleSocketPath(t *testing.T) {
	bin := faketool.Bin(t)
	faketool.Herdr{Responses: []faketool.HerdrResponse{
		sessionListResponse(`{"default":false,"name":"hand-f_fleet","running":true,"socket_path":"/stale/socket"}`),
		{Command: "status server", Stderr: "transport unavailable", Exit: 1},
	}}.Install(t, bin)

	got := observeTestSession(t)
	if got.State != SessionUnknown {
		t.Fatalf("observation = %+v, want unknown when status cannot prove the stale socket", got)
	}
}

func TestObserveFleetSessionUsesExactNamedManagedInvocationWithoutMutation(t *testing.T) {
	logPath := t.TempDir() + "/herdr.log"
	bin := faketool.Bin(t)
	faketool.Herdr{
		Responses: []faketool.HerdrResponse{
			sessionListResponse(`{"default":true,"name":"default","running":true},{"default":false,"name":"hand-f_fleet","running":true}`),
			serverStatusResponse("running", true, true, "hand-f_fleet"),
		},
		Log: logPath,
	}.Install(t, bin)

	got := observeTestSession(t)
	if got.State != SessionRunningCompatible {
		t.Fatalf("observation = %+v, want running-compatible", got)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "herdr --session hand-f_fleet session list --json\nherdr --session hand-f_fleet status server --json\n"
	if string(log) != want {
		t.Fatalf("Herdr calls = %q, want %q", log, want)
	}
	for _, mutation := range []string{"server", "stop", "delete", "attach"} {
		if strings.Contains(string(log), mutation) && mutation != "server" {
			t.Fatalf("Herdr calls = %q, observer attempted mutation %q", log, mutation)
		}
	}
}

func TestServerNotRunningIsTypedAndUserFacingErrorIsBounded(t *testing.T) {
	faketool.Herdr{Responses: []faketool.HerdrResponse{{
		Command: "workspace list",
		Stdout:  `{"error":{"code":"server_not_running","message":"the server is not running"}}`,
		Exit:    1,
	}}}.Install(t, faketool.Bin(t))

	_, err := NewSessionClient("hand-f_fleet").WorkspaceList()
	if !IsServerNotRunning(err) || !errors.Is(err, ErrServerNotRunning) {
		t.Fatalf("error = %v, want typed server-not-running condition", err)
	}
	if got, want := err.Error(), `Fleet Herdr session "hand-f_fleet" is not running`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "server_not_running") || strings.Contains(err.Error(), `"error"`) {
		t.Fatalf("error = %q, must not expose provider envelope", err)
	}
}

func ptr[T any](value T) *T { return &value }
