package transcript

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
)

var reexport = 5 * time.Second

const endedRetry = 10 * time.Minute

func (s *session) cached(stamp time.Time, ended bool) (bool, error) {
	if s.failure != nil {
		window := reexport
		if ended {
			window = endedRetry
		}
		if time.Since(s.failedAt) < window {
			return true, s.failure
		}
	}
	switch {
	case s.exported.IsZero():
		return false, nil
	case ended:
		return s.final, nil
	}
	return time.Since(s.exported) < reexport && (stamp.IsZero() || stamp.Equal(s.stamp)), nil
}

func (r *Reader) gate(ctx context.Context, key string) (func(), error) {
	r.mu.Lock()
	if r.gates == nil {
		r.gates = map[string]chan struct{}{}
	}
	gate := r.gates[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		r.gates[key] = gate
	}
	r.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Reader) fetch(ctx context.Context, name, id, dir string, ended bool) error {
	if name != "opencode" || !sessionID.MatchString(id) {
		return nil
	}
	if r.Paths.Opencode == "" {
		return errors.New("opencode is not on PATH")
	}
	key := name + ":" + id
	release, err := r.gate(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	r.mu.Lock()
	hit, failure := r.session(key).cached(dbStamp(r.Paths.OpencodeData), ended)
	r.mu.Unlock()
	if hit {
		return failure
	}
	fresh, err := r.export(ctx, id, dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			s := r.session(key)
			s.failure, s.failedAt = err, time.Now()
		}
		return err
	}
	fresh.final = ended
	*r.session(key) = fresh
	return nil
}

func (r *Reader) export(ctx context.Context, id, dir string) (session, error) {
	out, err := harness.Opencode(ctx, r.Paths.Opencode, dir, "session", "export", "--standalone", id)
	if err != nil {
		return session{}, err
	}
	var export struct {
		Info struct {
			Model *exportModel `json:"model"`
		} `json:"info"`
		Messages []exportMessage `json:"messages"`
	}
	if json.Unmarshal(out, &export) != nil || export.Messages == nil {
		return session{}, ErrUnreadable
	}
	fresh := session{offsets: map[string]int64{}, exported: time.Now(), stamp: dbStamp(r.Paths.OpencodeData)}
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
	return fresh, nil
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

func OpencodeSessions(ctx context.Context, bin, dir string, now time.Time, stale time.Duration) (map[string]ModelLabel, map[string]bool, map[string]time.Time, error) {
	out, err := harness.Opencode(ctx, bin, dir, "api", "--standalone", "GET", "/api/session?limit=1000")
	if err != nil {
		return nil, nil, nil, err
	}
	var list struct {
		Data []struct {
			ID     string       `json:"id"`
			Parent string       `json:"parentID"`
			Model  *exportModel `json:"model"`
			Time   struct {
				Updated int64           `json:"updated"`
				Idle    int64           `json:"idle"`
				Busy    json.RawMessage `json:"busy"`
			} `json:"time"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &list) != nil || list.Data == nil {
		return nil, nil, nil, ErrUnreadable
	}
	models := make(map[string]ModelLabel, len(list.Data))
	parent := make(map[string]string, len(list.Data))
	for _, s := range list.Data {
		parent[s.ID] = s.Parent
		if s.ID == "" {
			continue
		}
		var l ModelLabel
		if s.Model != nil {
			l.Model, l.Effort = s.Model.label()
		}
		models[s.ID] = l
	}
	busy := map[string]bool{}
	idle := map[string]time.Time{}
	for _, s := range list.Data {
		t := s.Time
		if t.Idle != 0 {
			idle[s.ID] = time.UnixMilli(t.Idle)
		}
		running := t.Idle == 0 || s.Parent != "" && t.Idle < t.Updated
		if t.Busy != nil {
			started, _ := strconv.ParseInt(string(t.Busy), 10, 64)
			running = started != 0 && started > t.Idle
		}
		if !running || now.Sub(time.UnixMilli(t.Updated)) > stale {
			continue
		}
		for id := s.ID; id != "" && !busy[id]; id = parent[id] {
			busy[id] = true
		}
	}
	return models, busy, idle, nil
}

func OpencodeSubmitted(ctx context.Context, bin, dir, session string) (bool, error) {
	_, found, err := OpencodeLastUser(ctx, bin, dir, session)
	return found, err
}

func OpencodeLastUser(ctx context.Context, bin, dir, session string) (string, bool, error) {
	out, err := harness.Opencode(ctx, bin, dir, "api", "--standalone", "session.message.list", "--param", "sessionID="+session, "--param", "type=user", "--param", "order=desc", "--param", "limit=1")
	if err != nil {
		return "", false, err
	}
	var list struct {
		Data []struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &list) != nil || list.Data == nil {
		return "", false, ErrUnreadable
	}
	if len(list.Data) == 0 {
		return "", false, nil
	}
	return list.Data[0].Text, true, nil
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
