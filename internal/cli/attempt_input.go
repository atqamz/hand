package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	attemptCommands["send"] = cmdAttemptSend
	attemptCommands["read"] = cmdAttemptRead
	attemptCommands["keys"] = cmdAttemptKeys
}

func runningAttempt(ctx context.Context, st *state.Store, id int64) (state.Attempt, error) {
	a, err := st.Attempt(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Status != state.AttemptRunning {
		return a, fmt.Errorf("%w: attempt %s is %s", state.ErrConflict, state.AttemptRef(id), a.Status)
	}
	return a, nil
}

func cmdAttemptSend(r *runner, args []string) error {
	fs := flags("attempt send")
	text := fs.String("text", "", "message for the worker")
	file := fs.String("file", "", "file holding the message")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	if (*text == "") == (*file == "") {
		return usageError{"attempt send: give exactly one of --text or --file"}
	}
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("%w: %v", state.ErrInvalid, err)
		}
		*text = string(b)
	}
	if strings.TrimSpace(*text) == "" {
		return fmt.Errorf("%w: message must not be empty", state.ErrInvalid)
	}
	id, err := parseID("a", pos[0])
	if err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		a, err := runningAttempt(ctx, st, id)
		if err != nil {
			return err
		}
		ref := state.AttemptRef(id)
		if err := c.Prompt(ctx, a.PaneID, *text); err != nil {
			if luvus.Code(err) != "agent_not_ready" {
				return runtimeErr(err)
			}
			desc := "unknown"
			if ag, err := c.Explain(ctx, a.PaneID); err == nil {
				desc = ag.Status
				if ag.Hint != "" {
					desc += ": " + ag.Hint
				}
			}
			return fmt.Errorf("%w: attempt %s is not at a prompt (%s); nothing was sent; read it with `hand attempt read %s`", state.ErrConflict, ref, desc, ref)
		}
		n := strconv.Itoa(len(*text))
		if err := st.NoteAttempt(ctx, id, "sent", n+" bytes"); err != nil {
			return err
		}
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("sent_bytes", n)
		return r.print(&d)
	})
}

func cmdAttemptRead(r *runner, args []string) error {
	fs := flags("attempt read")
	lines := fs.Int("lines", 40, "screen lines")
	pos, err := parse(fs, args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("a", pos[0])
	if err != nil {
		return err
	}
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		a, err := runningAttempt(ctx, st, id)
		if err != nil {
			return err
		}
		s, err := c.Read(ctx, a.PaneID, *lines)
		if err != nil {
			return runtimeErr(err)
		}
		if s.TerminalID != a.TerminalID {
			return fmt.Errorf("%w: pane %s now shows another terminal", state.ErrConflict, a.PaneID)
		}
		ref, rev := state.AttemptRef(id), strconv.FormatInt(s.ContentRevision, 10)
		var d toon.Doc
		d.Field("attempt", ref)
		d.Field("revision", rev)
		d.Field("screen", s.Text)
		d.Help("Answer exactly what the screen asks: `hand attempt keys --revision " + rev + " " + ref + " KEY...`")
		return r.print(&d)
	})
}

func cmdAttemptKeys(r *runner, args []string) error {
	fs := flags("attempt keys")
	revision := fs.Int64("revision", -1, "content revision printed by `hand attempt read`")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt keys: %v", err)}
	}
	if fs.NArg() < 2 || *revision < 0 {
		return usageError{"usage: hand attempt keys --revision N ATTEMPT KEY..."}
	}
	id, err := parseID("a", fs.Arg(0))
	if err != nil {
		return err
	}
	keys := fs.Args()[1:]
	return r.withAttempts(func(ctx context.Context, st *state.Store, c luvus.Client) error {
		a, err := runningAttempt(ctx, st, id)
		if err != nil {
			return err
		}
		if err := c.Keys(ctx, a.PaneID, keys, *revision, a.TerminalID); err != nil {
			if luvus.Code(err) == "content_revision_conflict" {
				return fmt.Errorf("%w: the screen changed since revision %d; nothing was sent; read it again", state.ErrConflict, *revision)
			}
			return runtimeErr(err)
		}
		joined := strings.Join(keys, " ")
		if err := st.NoteAttempt(ctx, id, "keys", joined); err != nil {
			return err
		}
		var d toon.Doc
		d.Field("attempt", state.AttemptRef(id))
		d.Field("keys", joined)
		return r.print(&d)
	})
}
