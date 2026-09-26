package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSendIsRefusedAtAPermissionPrompt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint, rt.ready = "blocked", "Do you want to proceed?", false })
	_, errOut, code := fx.h.run("attempt", "send", "--text", "keep going", "a1")
	if code != 3 || !strings.Contains(errOut, "not at a prompt (blocked: Do you want to proceed?)") || !strings.Contains(errOut, "nothing was sent") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := fx.rt.prompts(); len(got) != 0 {
		t.Fatalf("delivered at a prompt: %q", got)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint, rt.ready = "working", "", true })
	if out := fx.h.ok("attempt", "send", "--text", "keep going", "a1"); !strings.Contains(out, "sent_bytes: 10") {
		t.Fatalf("send = %q", out)
	}
	file := filepath.Join(t.TempDir(), "msg.md")
	if err := os.WriteFile(file, []byte("also run the tests"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("attempt", "send", "--file", file, "a1")
	if got := fx.rt.prompts(); !slices.Equal(got, []string{"keep going", "also run the tests"}) {
		t.Fatalf("delivered = %q", got)
	}
	if _, _, code := fx.h.run("attempt", "send", "a1"); code != 2 {
		t.Fatalf("no message code = %d, want 2", code)
	}
}

func TestKeysNeedTheRevisionTheOperatorRead(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.revision = 7 })
	read := fx.h.ok("attempt", "read", "a1")
	if !strings.Contains(read, "revision: 7") || !strings.Contains(read, `screen: "Do you want to proceed?\n❯ 1. Yes"`) {
		t.Fatalf("read = %q", read)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.revision = 8 })
	_, errOut, code := fx.h.run("attempt", "keys", "--revision", "7", "a1", "enter")
	if code != 3 || !strings.Contains(errOut, "the screen changed since revision 7") {
		t.Fatalf("stale keys code=%d stderr=%q", code, errOut)
	}
	if out := fx.h.ok("attempt", "keys", "--revision", "8", "a1", "enter"); !strings.Contains(out, "keys: enter") {
		t.Fatalf("keys = %q", out)
	}
	if got := fx.rt.keysSent(); !slices.Equal(got, []string{"enter"}) {
		t.Fatalf("keys sent = %q", got)
	}
	if _, _, code := fx.h.run("attempt", "keys", "a1", "enter"); code != 2 {
		t.Fatalf("keys without revision code = %d, want 2", code)
	}
}

func TestInputIsRefusedAfterARestart(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	fx.rt.srv.SetGeneration("gen-2")
	for _, args := range [][]string{
		{"attempt", "send", "--text", "hi", "a1"},
		{"attempt", "read", "a1"},
		{"attempt", "keys", "--revision", "0", "a1", "enter"},
	} {
		if _, errOut, code := fx.h.run(args...); code != 3 || !strings.Contains(errOut, "attempt a1 is interrupted") {
			t.Fatalf("%q code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := fx.rt.prompts(); len(got) != 0 {
		t.Fatalf("delivered after restart: %q", got)
	}
}
