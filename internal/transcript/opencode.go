package transcript

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
)

const reexport = 5 * time.Second

func (r *Reader) opencode(ctx context.Context, s *session, id, dir string) error {
	if r.Paths.Opencode == "" {
		return errors.New("opencode is not on PATH")
	}
	stamp := dbStamp(r.Paths.OpencodeData)
	switch {
	case s.exported.IsZero():
	case stamp.IsZero() && time.Since(s.exported) < reexport:
		return nil
	case !stamp.IsZero() && stamp.Equal(s.stamp):
		return nil
	}
	out, err := harness.Opencode(ctx, r.Paths.Opencode, dir, "session", "export", "--standalone", id)
	if err != nil {
		return err
	}
	var export struct {
		Info struct {
			Model *exportModel `json:"model"`
		} `json:"info"`
		Messages []exportMessage `json:"messages"`
	}
	if json.Unmarshal(out, &export) != nil || export.Messages == nil {
		return ErrUnreadable
	}
	fresh := session{offsets: map[string]int64{}, exported: time.Now(), stamp: stamp}
	for _, m := range export.Messages {
		fresh.lines++
		role, created, finish, texts := m.flat()
		if role == "" {
			continue
		}
		fresh.known++
		if role == "compaction" {
			fresh.status.Compactions++
		}
		for _, p := range append(m.Content, m.Parts...) {
			if p.Type == "compaction" {
				fresh.status.Compactions++
			}
		}
		if (role == "assistant" || role == "model-switched") && m.Model != nil && m.Model.ID != "" {
			fresh.status.Model, fresh.status.Effort = m.Model.label()
		}
		if t := cmp.Or(m.Tokens, m.Info.Tokens); role == "assistant" && t != nil && t.Input+t.Cache.Read+t.Cache.Write > 0 {
			fresh.status.Context = t.Input + t.Cache.Read + t.Cache.Write
		}
		at := time.UnixMilli(created).UTC().Format("2006-01-02T15:04:05.000Z")
		switch role {
		case "user":
			if m.Text != "" {
				texts = []string{m.Text}
			}
			fresh.user(strings.Join(texts, "\n"), at)
		case "assistant":
			for i, t := range texts {
				fresh.reply(t, at, finish == "stop" && i == len(texts)-1)
			}
		}
	}
	if fresh.status.Model == "" && export.Info.Model != nil {
		fresh.status.Model, fresh.status.Effort = export.Info.Model.label()
	}
	*s = fresh
	return nil
}

type exportModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant"`
}

func (m *exportModel) UnmarshalJSON(b []byte) error {
	type plain exportModel
	var p plain
	if json.Unmarshal(b, &p) == nil {
		*m = exportModel(p)
	}
	return nil
}

type ModelLabel struct{ Model, Effort string }

func OpencodeModels(ctx context.Context, bin, dir string) (map[string]ModelLabel, error) {
	out, err := harness.Opencode(ctx, bin, dir, "api", "--standalone", "GET", "/api/session?limit=1000")
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID    string       `json:"id"`
			Model *exportModel `json:"model"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &list) != nil || list.Data == nil {
		return nil, ErrUnreadable
	}
	models := make(map[string]ModelLabel, len(list.Data))
	for _, s := range list.Data {
		if s.ID == "" {
			continue
		}
		var l ModelLabel
		if s.Model != nil {
			l.Model, l.Effort = s.Model.label()
		}
		models[s.ID] = l
	}
	return models, nil
}

func (m exportModel) label() (model, effort string) {
	if m.ID == "" {
		return "", ""
	}
	if m.Variant != "default" {
		effort = m.Variant
	}
	if m.ProviderID == "" {
		return m.ID, effort
	}
	return m.ProviderID + "/" + m.ID, effort
}

type exportPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type exportStamp struct {
	Created int64 `json:"created"`
}

type exportMessage struct {
	Type    string        `json:"type"`
	Text    string        `json:"text"`
	Finish  string        `json:"finish"`
	Time    exportStamp   `json:"time"`
	Tokens  *exportTokens `json:"tokens"`
	Model   *exportModel  `json:"model"`
	Content []exportPart  `json:"content"`
	Info    struct {
		Role   string        `json:"role"`
		Finish string        `json:"finish"`
		Time   exportStamp   `json:"time"`
		Tokens *exportTokens `json:"tokens"`
	} `json:"info"`
	Parts []exportPart `json:"parts"`
}

type exportTokens struct {
	Input int64 `json:"input"`
	Cache struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

func (m exportMessage) flat() (role string, created int64, finish string, texts []string) {
	role, created, finish = m.Type, m.Time.Created, m.Finish
	if role == "" {
		role, created, finish = m.Info.Role, m.Info.Time.Created, m.Info.Finish
	}
	for _, p := range append(m.Content, m.Parts...) {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return role, created, finish, texts
}

func dbStamp(dir string) time.Time {
	var newest time.Time
	for _, name := range []string{"opencode.db", "opencode.db-wal"} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	return newest
}
