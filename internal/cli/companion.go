package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/qr"
	"github.com/atqamz/hand/internal/toon"
)

const companionCodeUsage = "usage: hand companion code --base https://host.example"

var companionCommands = map[string]handler{
	"code": cmdCompanionCode,
}

func init() {
	commands["companion"] = func(r *runner, args []string) error {
		return sub(r, args, "companion", companionCommands)
	}
}

func cmdCompanionCode(r *runner, args []string) error {
	set := flags("companion code")
	base := set.String("base", "", "the address the Companion opens, scheme and host only, like https://host.example")
	if err := set.Parse(args); err != nil {
		return helpOr(set, err, usageError{fmt.Sprintf("companion code: %v; %s", err, companionCodeUsage)})
	}
	if set.NArg() > 0 || *base == "" {
		return usageError{companionCodeUsage}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	_ = st.Close()
	token, err := boardToken(r.home)
	if err != nil {
		return err
	}
	ctx := r.ctx()
	addr, err := boardAddr(ctx, r.root, r.fleet.ID, token)
	if err != nil {
		return err
	}
	code, err := mintPhoneCode(ctx, "http://"+addr+"/"+r.fleet.ID+"/api/v1", "hand_board_"+r.fleet.ID, token, *base)
	if err != nil {
		return err
	}
	matrix, err := qr.Matrix(code.Link)
	if err != nil {
		return errors.New("the pairing link is too long for a QR code; use a shorter --base")
	}
	var d toon.Doc
	d.Field("link", code.Link)
	d.Field("code", code.Code)
	d.Field("expires", code.ExpiresAt)
	if err := r.print(&d); err != nil {
		return err
	}
	color := r.terminal() && r.env.Getenv("NO_COLOR") == ""
	_, err = io.WriteString(r.env.Stdout, "qr: |\n"+renderQR(matrix, color))
	return err
}

func mintPhoneCode(ctx context.Context, api, cookie, token, base string) (*board.APIPhoneCode, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var sess board.APISession
	if err := boardCall(ctx, http.MethodGet, api+"/session", cookie, token, "", nil, &sess); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"base": base})
	if err != nil {
		return nil, err
	}
	var phone board.APIPhone
	if err := boardCall(ctx, http.MethodPost, api+"/phone/code", cookie, token, sess.CSRF, body, &phone); err != nil {
		return nil, err
	}
	if phone.Code == nil {
		return nil, errors.New("the board made no pairing code")
	}
	return phone.Code, nil
}

func boardCall(ctx context.Context, method, url, cookie, token, csrf string, body []byte, data any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: cookie, Value: token})
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("the board did not answer; start it with `hand board`")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return errors.New("the board's answer could not be read")
	}
	if res.StatusCode != http.StatusOK {
		var failed board.APIErrorResponse
		msg := ""
		if json.Unmarshal(raw, &failed) == nil {
			msg = strings.Join(strings.Fields(failed.Error.Message), " ")
		}
		if msg == "" {
			msg = http.StatusText(res.StatusCode)
		}
		return fmt.Errorf("the board refused (%d): %s", res.StatusCode, msg)
	}
	env := board.APIEnvelope{Data: data}
	if err := json.Unmarshal(raw, &env); err != nil {
		return errors.New("the board's answer is not an API envelope")
	}
	return nil
}

func renderQR(m [][]bool, color bool) string {
	const quiet = 4
	size := len(m) + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return y >= 0 && y < len(m) && x >= 0 && x < len(m) && m[y][x]
	}
	glyph := [4]string{" ", "▄", "▀", "█"}
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		b.WriteString("  ")
		if color {
			b.WriteString("\x1b[30;47m")
		}
		for x := range size {
			i := 0
			if dark(x, y) {
				i += 2
			}
			if dark(x, y+1) {
				i++
			}
			b.WriteString(glyph[i])
		}
		if color {
			b.WriteString("\x1b[0m")
		}
		b.WriteByte('\n')
	}
	return b.String()
}
