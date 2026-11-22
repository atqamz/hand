package desktop

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ReadModel interface {
	Fleets() []Fleet
	Name(ctx context.Context, fleet string) (string, error)
	Status(ctx context.Context, fleet string) (Status, error)
	Timeline(ctx context.Context, fleet string, before int) (Timeline, error)
	Tasks(ctx context.Context, fleet string, all bool) (Tasks, error)
	TaskSearch(ctx context.Context, fleet string, q TaskSearchQuery) (TaskSearch, error)
	Projects(ctx context.Context, fleet string) (Projects, error)
	Task(ctx context.Context, fleet, ref string) (TaskDetail, error)
	Attempt(ctx context.Context, fleet, ref string) (AttemptDetail, error)
	Decision(ctx context.Context, fleet, ref string) (DecisionDetail, error)
	Report(ctx context.Context, fleet, ref string) (ReportDetail, error)
	Doc(ctx context.Context, fleet, path string, page int) (Doc, error)
	DocWindow(ctx context.Context, fleet, path string, from int) (DocWindow, error)
	DocBytes(ctx context.Context, fleet, path string) ([]byte, error)
	AnswerDecision(ctx context.Context, fleet, ref, choice, note string) (Answered, error)
	AckReport(ctx context.Context, fleet, ref string) (Acked, error)
	SendMessage(ctx context.Context, fleet, text string, attachments []string) (Sent, error)
	UploadInbox(ctx context.Context, fleet, name string, body []byte) (InboxFile, error)
	InboxHead(ctx context.Context, fleet, name string) (int64, error)
	InboxBytes(ctx context.Context, fleet, name string) ([]byte, error)
	Supervise(ctx context.Context, fleet, action, ref string) (Control, error)
	StartWith(ctx context.Context, fleet, harness, model, effort string) (Control, error)
	Switch(ctx context.Context, fleet string, sw Switch) (Control, error)
	Models(ctx context.Context, fleet string) (Models, error)
	RefreshModels(ctx context.Context, fleet, harness string) (Refreshed, error)
	PressKeys(ctx context.Context, fleet string, keys []string, revision int64, digest string, after int64, ref string) (Control, error)
	PressAttemptKeys(ctx context.Context, fleet, ref, key string, revision, after int64, screen string) (Pressed, error)
	Poll(ctx context.Context, fleet string, screen Screen, ref string, since PollCursors) (Poll, error)
	Attention(ctx context.Context) ([]FleetSignal, error)
	Features(ctx context.Context, fleet string) ([]string, error)
	Links(ctx context.Context, fleet string, paths []string) ([]*Link, error)
}

type ReadCursor struct {
	Since       string
	Fresh, Skip bool
}

type PollCursors struct {
	Status, Screen, Timeline, Tasks ReadCursor
	Worker                          bool
}

type Poll struct {
	StatusFresh    bool
	StatusRead     bool
	StatusErr      error
	Status         Status
	StatusCursor   string
	ScreenFresh    bool
	ScreenRead     bool
	ScreenErr      error
	ScreenCursor   string
	TasksFresh     bool
	TasksRead      bool
	TasksErr       error
	TasksCursor    string
	Tasks          Tasks
	TimelineFresh  bool
	TimelineRead   bool
	TimelineErr    error
	TimelineCursor string
	Timeline       Timeline
	Task           TaskDetail
	Attempt        AttemptDetail
	Decision       DecisionDetail
}

// Control is a supervisor control's answer. Stale is true when the board ran
// the action but could not read the status back, so the screen must refresh.
type Control struct {
	Status        Status
	Stale         bool
	Receipt, Warn string
}

// Sent is a message the board accepted. Fresh is false when the board queued
// the text but could not read the conversation back.
type Sent struct {
	Timeline Timeline
	Fresh    bool
}

// Answered is an answer's reply: the decision's detail, empty when the board
// saved the answer but could not read it back, and the board's warning.
type Answered struct {
	DecisionDetail
	Warn    string
	Receipt string
}

// Acked is a report's reply: the report's detail, empty when the board marked
// it read but could not read it back, and the board's warning.
type Acked struct {
	ReportDetail
	Warn    string
	Receipt string
}

// Pressed is an attempt's key press reply: the attempt's detail, empty when
// the board sent the keys but could not read it back, and the board's warning.
type Pressed struct {
	AttemptDetail
	Warn    string
	Receipt string
}

const (
	apiVersion = 1
	maxBody    = 16 << 20
	docRawMax  = 10 << 20
)

var (
	fleetIDShape = regexp.MustCompile(`^f[0-9a-f]{12}$`)
	// ErrNoBoard says why a window has nothing to read.
	ErrNoBoard = errors.New("no board is running; start it with `hand open` in a fleet")
)

const (
	CodeRevoked       = "revoked"
	CodeNotRecognised = "not_recognised"
	CodeKeyChanged    = "key_changed"
)

type HTTPClient struct {
	Base   string
	BaseOf func() string
	Known  []Fleet
	Token  func(fleet string) (string, error)
	HTTP   *http.Client
	Paired bool
	Check  func() error
	Lost   func() error
	Down   error

	mu      sync.Mutex
	cursors map[string]string
}

func (c *HTTPClient) base() string {
	if c.BaseOf != nil {
		return c.BaseOf()
	}
	return c.Base
}

func (c *HTTPClient) cookie(fleet string) string {
	if c.Paired {
		return "hand_phone_" + fleet
	}
	return "hand_board_" + fleet
}

func (c *HTTPClient) Fleets() []Fleet {
	return append([]Fleet(nil), c.Known...)
}

func NewLocal(getenv func(string) string) (*HTTPClient, error) {
	c, err := openLocal(getenv)
	if err != nil {
		return nil, err
	}
	if c.Down != nil {
		return nil, c.Down
	}
	return c, nil
}

func openLocal(getenv func(string) string) (*HTTPClient, error) {
	root, err := localRoot(getenv, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	base, down := localBoard(root)
	fleets, tokens, err := localFleets(root)
	if err != nil {
		if down != nil {
			return nil, down
		}
		return nil, err
	}
	return &HTTPClient{
		Base:  base,
		Down:  down,
		Known: fleets,
		Token: func(fleet string) (string, error) { return readBoardToken(tokens[fleet]) },
		HTTP:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func localRoot(getenv func(string) string, goos string) (string, error) {
	root := getenv("SECONDHAND_HOME")
	if root == "" {
		home := getenv("HOME")
		if home == "" && goos == "windows" {
			home = getenv("USERPROFILE")
		}
		if home == "" {
			return "", errors.New("set HOME or SECONDHAND_HOME; the desktop reads the board's address and tokens there")
		}
		root = filepath.Join(home, ".secondhand")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if errors.Is(err, os.ErrNotExist) {
		return abs, nil
	}
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func localBoard(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "board.addr"))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoBoard
	}
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(b))
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "", fmt.Errorf("%w: board.addr holds %q, which is not an address", ErrNoBoard, addr)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("the board listens on %s, and its API answers only on a loopback address", addr)
	}
	return "http://" + addr, nil
}

func localFleets(root string) ([]Fleet, map[string]string, error) {
	var fleets []Fleet
	tokens := map[string]string{}
	seen := map[string]bool{}
	for _, dir := range []string{filepath.Join(root, "index"), filepath.Join(root, "fleets")} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range entries {
			id := entry.Name()
			if !fleetIDShape.MatchString(id) || seen[id] {
				continue
			}
			home, err := fleetHome(dir, id)
			if err != nil {
				continue
			}
			if info, err := os.Stat(filepath.Join(home, "hand.db")); err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[id] = true
			token := filepath.Join(home, "board.token")
			tokens[id] = token
			fleets = append(fleets, Fleet{ID: id, Name: id, Home: home})
		}
	}
	sort.Slice(fleets, func(i, j int) bool { return fleets[i].ID < fleets[j].ID })
	return fleets, tokens, nil
}

func fleetHome(dir, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, id))
	if err != nil {
		return "", err
	}
	home := strings.TrimSpace(string(b))
	if home == "" {
		return "", fmt.Errorf("the fleet %s link is empty", id)
	}
	return home, nil
}

func readBoardToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("no board.token yet; open it once with `hand open` in that fleet")
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no board.token yet; open it once with `hand open` in that fleet")
	}
	if err != nil {
		return "", fmt.Errorf("board.token cannot be read: %w; open it once with `hand open` in that fleet", err)
	}
	token := strings.TrimSpace(string(b))
	if len(token) != 48 {
		return "", errors.New("board.token is not a board token; delete it and open the fleet again")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("board.token is not a board token; delete it and open the fleet again")
	}
	return token, nil
}

func (c *HTTPClient) Name(ctx context.Context, fleet string) (string, error) {
	env, err := c.get(ctx, fleet, "/status", &Status{})
	if err != nil {
		return "", err
	}
	return env.Fleet.Name, nil
}

func (c *HTTPClient) Status(ctx context.Context, fleet string) (Status, error) {
	var out Status
	_, err := c.get(ctx, fleet, "/status", &out)
	return out, err
}

func (c *HTTPClient) Timeline(ctx context.Context, fleet string, before int) (Timeline, error) {
	path := "/timeline"
	if before > 0 {
		path += fmt.Sprintf("?before=%d", before)
	}
	var out Timeline
	_, err := c.get(ctx, fleet, path, &out)
	return out, err
}

func (c *HTTPClient) Tasks(ctx context.Context, fleet string, all bool) (Tasks, error) {
	path := "/tasks"
	if all {
		path += "?all=1"
	}
	var out Tasks
	_, err := c.get(ctx, fleet, path, &out)
	return out, err
}

func (c *HTTPClient) TaskSearch(ctx context.Context, fleet string, q TaskSearchQuery) (TaskSearch, error) {
	v := url.Values{}
	for key, value := range map[string]string{"q": q.Q, "status": q.Status, "project": q.Project, "since": q.Since, "until": q.Until} {
		if value != "" {
			v.Set(key, value)
		}
	}
	if q.Page > 1 {
		v.Set("page", strconv.Itoa(q.Page))
	}
	path := "/tasks/search"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return TaskSearch{}, err
	}
	var out TaskSearch
	_, err = c.getWith(ctx, fleet, token, path, &out)
	return out, err
}

func (c *HTTPClient) Links(ctx context.Context, fleet string, paths []string) ([]*Link, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return nil, err
	}
	var out struct {
		Links []*Link `json:"links"`
	}
	if _, err = c.getWith(ctx, fleet, token, "/links?"+url.Values{"path": paths}.Encode(), &out); err != nil {
		return nil, err
	}
	return out.Links, nil
}

func (c *HTTPClient) Features(ctx context.Context, fleet string) ([]string, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/v1/meta", nil)
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: c.cookie(fleet), Value: token})
	res, err := c.client().Do(req)
	if err != nil {
		return nil, c.unanswered(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.failure(res)
	}
	var env struct {
		Version int `json:"version"`
		Data    struct {
			Features []string `json:"features"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&env); err != nil {
		return nil, fmt.Errorf("the board's answer is not an envelope: %v", err)
	}
	if env.Version != apiVersion {
		return nil, fmt.Errorf("the board speaks API version %d; this window reads version %d", env.Version, apiVersion)
	}
	return env.Data.Features, nil
}

func (c *HTTPClient) Projects(ctx context.Context, fleet string) (Projects, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return Projects{}, err
	}
	var out Projects
	_, err = c.getWith(ctx, fleet, token, "/projects", &out)
	return out, err
}

func (c *HTTPClient) Task(ctx context.Context, fleet, ref string) (TaskDetail, error) {
	var out TaskDetail
	_, err := c.get(ctx, fleet, "/task/"+url.PathEscape(ref), &out)
	return out, err
}

func (c *HTTPClient) Attempt(ctx context.Context, fleet, ref string) (AttemptDetail, error) {
	var out AttemptDetail
	_, err := c.get(ctx, fleet, "/attempt/"+url.PathEscape(ref), &out)
	return out, err
}

func (c *HTTPClient) Decision(ctx context.Context, fleet, ref string) (DecisionDetail, error) {
	var out DecisionDetail
	_, err := c.get(ctx, fleet, "/decision/"+url.PathEscape(ref), &out)
	return out, err
}

func (c *HTTPClient) Report(ctx context.Context, fleet, ref string) (ReportDetail, error) {
	var out ReportDetail
	_, err := c.get(ctx, fleet, "/report/"+url.PathEscape(ref), &out)
	return out, err
}

func docPath(path string) string {
	return "/doc/" + (&url.URL{Path: path}).EscapedPath()
}

func (c *HTTPClient) docGet(ctx context.Context, fleet, path string, out any) error {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return err
	}
	_, err = c.getWith(ctx, fleet, token, path, out)
	return err
}

func (c *HTTPClient) Doc(ctx context.Context, fleet, path string, page int) (Doc, error) {
	q := ""
	if page > 1 {
		q = "?page=" + strconv.Itoa(page)
	}
	var out Doc
	err := c.docGet(ctx, fleet, docPath(path)+q, &out)
	return out, err
}

func (c *HTTPClient) DocWindow(ctx context.Context, fleet, path string, from int) (DocWindow, error) {
	var out DocWindow
	err := c.docGet(ctx, fleet, docPath(path)+"?from="+strconv.Itoa(max(from, 1)), &out)
	return out, err
}

func (c *HTTPClient) DocBytes(ctx context.Context, fleet, path string) ([]byte, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return nil, err
	}
	res, err := c.do(ctx, fleet, token, "", "", http.MethodGet, docPath(path)+"?raw", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.failure(res)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, docRawMax+1))
	if err == nil && len(b) > docRawMax {
		return nil, errors.New("the file is larger than this window reads")
	}
	return b, err
}

func (c *HTTPClient) Poll(ctx context.Context, fleet string, screen Screen, ref string, since PollCursors) (Poll, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return Poll{}, err
	}
	var p Poll
	p.StatusFresh, p.StatusCursor, p.StatusRead, p.StatusErr = c.pollRead(ctx, fleet, token, "/status", since.Status, since.Worker, &p.Status)
	p.ScreenFresh, p.ScreenCursor, p.ScreenRead, p.ScreenErr = c.pollRead(ctx, fleet, token, pollPath(screen, ref), since.Screen, false, pollData(screen, &p))
	if screen != BoardScreen && screen != TasksScreen {
		p.TasksFresh, p.TasksCursor, p.TasksRead, p.TasksErr = c.pollRead(ctx, fleet, token, "/tasks?all=1", since.Tasks, false, &p.Tasks)
	}
	p.TimelineFresh, p.TimelineCursor, p.TimelineRead, p.TimelineErr = c.pollRead(ctx, fleet, token, "/timeline", since.Timeline, false, &p.Timeline)
	if ctx.Err() != nil {
		return p, errors.Join(p.StatusErr, p.ScreenErr, p.TasksErr, p.TimelineErr)
	}
	return p, nil
}

func (c *HTTPClient) pollRead(ctx context.Context, fleet, token, path string, rc ReadCursor, full bool, out any) (fresh bool, cursor string, read bool, err error) {
	if rc.Skip || path == "" {
		return
	}
	since := rc.Since
	if since == "" && !rc.Fresh && !full {
		since = c.cursor(fleet, path)
	}
	fresh, cursor, err = c.pollAs(ctx, fleet, token, path, since, out)
	return fresh, cursor, err == nil, err
}

func pollPath(screen Screen, ref string) string {
	switch screen {
	case TasksScreen, DocScreen:
		return ""
	case TaskScreen:
		return "/task/" + url.PathEscape(ref)
	case AttemptScreen:
		return "/attempt/" + url.PathEscape(ref)
	case DecisionScreen:
		return "/decision/" + url.PathEscape(ref)
	}
	return "/tasks?all=1"
}

func pollData(screen Screen, p *Poll) any {
	switch screen {
	case TaskScreen:
		return &p.Task
	case AttemptScreen:
		return &p.Attempt
	case DecisionScreen:
		return &p.Decision
	}
	return &p.Tasks
}

func (c *HTTPClient) pollAs(ctx context.Context, fleet, token, path, since string, out any) (bool, string, error) {
	fresh, cursor, err := c.pollSince(ctx, fleet, token, path, since, out)
	var api *APIError
	if errors.As(err, &api) && api.Code == "bad_cursor" {
		c.forget(fleet, path)
		return c.pollSince(ctx, fleet, token, path, "", out)
	}
	return fresh, cursor, err
}

func (c *HTTPClient) pollSince(ctx context.Context, fleet, token, path, since string, out any) (bool, string, error) {
	res, err := c.do(ctx, fleet, token, "", "", http.MethodGet, withSince(path, since), nil)
	if err != nil {
		return false, "", err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNotModified:
		return false, "", nil
	case http.StatusOK:
		env, err := c.decode(res, fleet, path, out)
		if err != nil {
			return false, "", err
		}
		return true, env.Cursor, nil
	}
	return false, "", c.failure(res)
}

func withSince(path, since string) string {
	if since == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "since=" + url.QueryEscape(since)
}

func cursorKey(fleet, path string) string {
	return fleet + "\x00" + path
}

func (c *HTTPClient) cursor(fleet, path string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursors[cursorKey(fleet, path)]
}

func (c *HTTPClient) remember(fleet, path, cursor string) {
	if cursor == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cursors == nil {
		c.cursors = map[string]string{}
	}
	c.cursors[cursorKey(fleet, path)] = cursor
}

func (c *HTTPClient) forget(fleet, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cursors, cursorKey(fleet, path))
}

type answerRequest struct {
	Choice string `json:"choice"`
	Note   string `json:"note"`
}

func (c *HTTPClient) AnswerDecision(ctx context.Context, fleet, ref, choice, note string) (Answered, error) {
	var out DecisionDetail
	env, err := c.post(ctx, fleet, "/decision/"+url.PathEscape(ref)+"/answer", answerRequest{Choice: choice, Note: note}, &out)
	return Answered{DecisionDetail: out, Warn: env.Warn, Receipt: env.Receipt}, err
}

func (c *HTTPClient) AckReport(ctx context.Context, fleet, ref string) (Acked, error) {
	var out ReportDetail
	env, err := c.post(ctx, fleet, "/report/"+url.PathEscape(ref)+"/ack", nil, &out)
	return Acked{ReportDetail: out, Warn: env.Warn, Receipt: env.Receipt}, err
}

type messageRequest struct {
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

type messageReply struct {
	Accepted bool `json:"accepted"`
	Timeline
}

func (c *HTTPClient) SendMessage(ctx context.Context, fleet, text string, attachments []string) (Sent, error) {
	var out messageReply
	_, err := c.post(ctx, fleet, "/message", messageRequest{Text: text, Attachments: attachments}, &out)
	return Sent{Timeline: out.Timeline, Fresh: !out.Accepted}, err
}

func inboxPath(name string) string { return "/inbox/" + url.PathEscape(name) }

func (c *HTTPClient) UploadInbox(ctx context.Context, fleet, name string, body []byte) (InboxFile, error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		return InboxFile{}, err
	}
	if _, err := part.Write(body); err != nil {
		return InboxFile{}, err
	}
	if err := form.Close(); err != nil {
		return InboxFile{}, err
	}
	var out InboxFile
	_, err = c.postRaw(ctx, fleet, "/inbox", form.FormDataContentType(), &buf, &out)
	return out, err
}

func (c *HTTPClient) InboxHead(ctx context.Context, fleet, name string) (int64, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return 0, err
	}
	res, err := c.do(ctx, fleet, token, "", "", http.MethodHead, inboxPath(name), nil)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		return res.ContentLength, nil
	case http.StatusNotFound:
		return 0, &APIError{Status: res.StatusCode, Code: "not_found", Message: "not found: file in inbox/"}
	}
	return 0, fmt.Errorf("the board answered %s", res.Status)
}

func (c *HTTPClient) InboxBytes(ctx context.Context, fleet, name string) ([]byte, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return nil, err
	}
	res, err := c.do(ctx, fleet, token, "", "", http.MethodGet, inboxPath(name), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.failure(res)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, docRawMax+1))
	if err == nil && len(b) > docRawMax {
		return nil, errors.New("the file is larger than this window reads")
	}
	return b, err
}

var supervisorActions = map[string]bool{"start": true, "resume": true, "stop": true, "interrupt": true, "force": true}

type controlRequest struct {
	Ref string `json:"ref"`
}

type controlReply struct {
	Status
	Accepted bool `json:"accepted"`
}

func (r controlReply) control() Control {
	return Control{Status: r.Status, Stale: r.Accepted}
}

func (c *HTTPClient) Supervise(ctx context.Context, fleet, action, ref string) (Control, error) {
	if !supervisorActions[action] {
		return Control{}, fmt.Errorf("%q is not a supervisor control", action)
	}
	var body any
	if action == "stop" || action == "interrupt" || action == "force" {
		if ref == "" {
			return Control{}, fmt.Errorf("%s needs the supervisor the status showed", action)
		}
		body = controlRequest{Ref: ref}
	}
	var out controlReply
	env, err := c.post(ctx, fleet, "/supervisor/"+action, body, &out)
	ctl := out.control()
	ctl.Receipt, ctl.Warn = env.Receipt, env.Warn
	return ctl, err
}

type startChoice struct {
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

func (c *HTTPClient) StartWith(ctx context.Context, fleet, harness, model, effort string) (Control, error) {
	if harness == "" {
		return Control{}, errors.New("a first supervisor needs a harness")
	}
	var out controlReply
	env, err := c.post(ctx, fleet, "/supervisor/start", startChoice{Harness: harness, Model: model, Effort: effort}, &out)
	ctl := out.control()
	ctl.Receipt, ctl.Warn = env.Receipt, env.Warn
	return ctl, err
}

func (c *HTTPClient) Switch(ctx context.Context, fleet string, sw Switch) (Control, error) {
	if sw.Ref == "" {
		return Control{}, errors.New("a switch needs the supervisor the status showed")
	}
	var out controlReply
	env, err := c.post(ctx, fleet, "/supervisor/switch", sw, &out)
	ctl := out.control()
	ctl.Receipt, ctl.Warn = env.Receipt, env.Warn
	return ctl, err
}

func (c *HTTPClient) Models(ctx context.Context, fleet string) (Models, error) {
	var out Models
	_, err := c.get(ctx, fleet, "/models", &out)
	return out, err
}

type refreshChoice struct {
	Harness string `json:"harness,omitempty"`
}

type refreshReply struct {
	Models
	Accepted bool `json:"accepted"`
}

func (c *HTTPClient) RefreshModels(ctx context.Context, fleet, harness string) (Refreshed, error) {
	var body any
	if harness != "" {
		body = refreshChoice{Harness: harness}
	}
	var out refreshReply
	env, err := c.post(ctx, fleet, "/models/refresh", body, &out)
	return Refreshed{Models: out.Models, Stale: out.Accepted, Receipt: env.Receipt, Warn: env.Warn}, err
}

type keysRequest struct {
	Keys     []string `json:"keys"`
	Revision int64    `json:"revision"`
	Digest   string   `json:"digest"`
	After    int64    `json:"after"`
	Ref      string   `json:"ref"`
}

func (c *HTTPClient) PressKeys(ctx context.Context, fleet string, keys []string, revision int64, digest string, after int64, ref string) (Control, error) {
	var out controlReply
	env, err := c.post(ctx, fleet, "/supervisor/keys", keysRequest{Keys: keys, Revision: revision, Digest: digest, After: after, Ref: ref}, &out)
	ctl := out.control()
	ctl.Receipt, ctl.Warn = env.Receipt, env.Warn
	return ctl, err
}

type attemptKeysRequest struct {
	Key      string `json:"key"`
	Revision int64  `json:"revision"`
	After    int64  `json:"after"`
	Screen   string `json:"screen,omitempty"`
}

func (c *HTTPClient) PressAttemptKeys(ctx context.Context, fleet, ref, key string, revision, after int64, screen string) (Pressed, error) {
	var out AttemptDetail
	env, err := c.post(ctx, fleet, "/attempt/"+url.PathEscape(ref)+"/keys", attemptKeysRequest{Key: key, Revision: revision, After: after, Screen: screen}, &out)
	return Pressed{AttemptDetail: out, Warn: env.Warn, Receipt: env.Receipt}, err
}

func (c *HTTPClient) get(ctx context.Context, fleet, path string, out any) (Envelope, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return Envelope{}, err
	}
	return c.getAs(ctx, fleet, token, path, out)
}

func (c *HTTPClient) getAs(ctx context.Context, fleet, token, path string, out any) (Envelope, error) {
	env, err := c.getWith(ctx, fleet, token, path, out)
	if err != nil {
		return Envelope{}, err
	}
	c.remember(fleet, path, env.Cursor)
	return env, nil
}

func (c *HTTPClient) getWith(ctx context.Context, fleet, token, path string, out any) (Envelope, error) {
	res, err := c.do(ctx, fleet, token, "", "", http.MethodGet, path, nil)
	if err != nil {
		return Envelope{}, err
	}
	defer res.Body.Close()
	return c.decode(res, fleet, path, out)
}

func (c *HTTPClient) post(ctx context.Context, fleet, path string, body, out any) (Envelope, error) {
	var raw io.Reader
	ctype := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Envelope{}, err
		}
		raw, ctype = bytes.NewReader(b), "application/json"
	}
	return c.postRaw(ctx, fleet, path, ctype, raw, out)
}

func (c *HTTPClient) postRaw(ctx context.Context, fleet, path, ctype string, raw io.Reader, out any) (Envelope, error) {
	token, err := c.prepare(ctx, fleet)
	if err != nil {
		return Envelope{}, err
	}
	var sess struct {
		CSRF string `json:"csrf"`
	}
	if _, err := c.getAs(ctx, fleet, token, "/session", &sess); err != nil {
		return Envelope{}, err
	}
	if sess.CSRF == "" {
		return Envelope{}, errors.New("the board's session has no CSRF token")
	}
	res, err := c.do(ctx, fleet, token, sess.CSRF, ctype, http.MethodPost, path, raw)
	if err != nil {
		return Envelope{}, err
	}
	defer res.Body.Close()
	env, err := c.decode(res, fleet, path, out)
	if err != nil && res.StatusCode == http.StatusOK {
		return Envelope{}, &AppliedUnread{Err: err}
	}
	return env, err
}

func (c *HTTPClient) Attention(ctx context.Context) ([]FleetSignal, error) {
	if c.Down != nil {
		return nil, c.Down
	}
	if c.Check != nil {
		if err := c.Check(); err != nil {
			return nil, err
		}
	}
	tokens := map[string]string{}
	var order []string
	var out []FleetSignal
	for _, f := range c.Known {
		if !fleetIDShape.MatchString(f.ID) {
			continue
		}
		token, err := c.Token(f.ID)
		if err != nil {
			continue
		}
		if !c.Paired {
			if err := c.prove(ctx, f.ID, token); err != nil {
				var refused refusedProof
				if !errors.As(err, &refused) {
					return nil, err
				}
				out = append(out, FleetSignal{Fleet: f.ID})
				continue
			}
		}
		tokens[f.ID] = token
		order = append(order, f.ID)
	}
	if len(order) == 0 {
		if len(out) == 0 {
			return nil, ErrNoBoard
		}
		return out, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/v1/fleets", nil)
	if err != nil {
		return nil, err
	}
	for _, id := range order {
		req.AddCookie(&http.Cookie{Name: c.cookie(id), Value: tokens[id]})
	}
	res, err := c.client().Do(req)
	if err != nil {
		return nil, c.unanswered(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.failure(res)
	}
	var env struct {
		Version int `json:"version"`
		Data    struct {
			Fleets []struct {
				ID          string `json:"id"`
				Unavailable bool   `json:"unavailable"`
				Needs       Needs  `json:"needs"`
			} `json:"fleets"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&env); err != nil {
		return nil, fmt.Errorf("the board's answer is not an envelope: %v", err)
	}
	if env.Version != apiVersion {
		return nil, fmt.Errorf("the board speaks API version %d; this window reads version %d", env.Version, apiVersion)
	}
	for _, f := range env.Data.Fleets {
		if _, ok := tokens[f.ID]; !ok {
			continue
		}
		out = append(out, FleetSignal{Fleet: f.ID, Signal: Signal{
			OK: !f.Unavailable, Waiting: f.Needs.Waiting, Worst: f.Needs.Worst, WorstWord: f.Needs.WorstWord,
		}})
	}
	return out, nil
}

func (c *HTTPClient) prepare(ctx context.Context, fleet string) (string, error) {
	if !fleetIDShape.MatchString(fleet) {
		return "", fmt.Errorf("%q is not a fleet id", fleet)
	}
	if c.Down != nil {
		return "", c.Down
	}
	if c.Check != nil {
		if err := c.Check(); err != nil {
			return "", err
		}
	}
	token, err := c.Token(fleet)
	if err != nil {
		return "", fmt.Errorf("fleet %s: %w", fleet, err)
	}
	if !c.Paired {
		if err := c.prove(ctx, fleet, token); err != nil {
			return "", err
		}
	}
	return token, nil
}

func (c *HTTPClient) do(ctx context.Context, fleet, token, csrf, ctype, method, path string, body io.Reader) (*http.Response, error) {
	u := c.base() + "/" + fleet + "/api/v1" + path
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: c.cookie(fleet), Value: token})
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := c.client().Do(req)
	if err != nil {
		return nil, c.unanswered(err)
	}
	return res, nil
}

func (c *HTTPClient) decode(res *http.Response, fleet, path string, out any) (Envelope, error) {
	if res.StatusCode != http.StatusOK {
		return Envelope{}, c.failure(res)
	}
	var env Envelope
	dec := json.NewDecoder(io.LimitReader(res.Body, maxBody))
	if err := dec.Decode(&env); err != nil {
		return Envelope{}, fmt.Errorf("the board's answer is not an envelope: %v", err)
	}
	if env.Version != apiVersion {
		return Envelope{}, fmt.Errorf("the board speaks API version %d; this window reads version %d", env.Version, apiVersion)
	}
	if env.Fleet.ID != fleet {
		return Envelope{}, fmt.Errorf("the board answered for fleet %s, not %s", env.Fleet.ID, fleet)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return Envelope{}, fmt.Errorf("the board's %s data: %v", path, err)
	}
	return env, nil
}

// prove asks the service at Base for HMAC(board.token, nonce) before the
// token goes out, so a service that took the board's port never sees it.
func (c *HTTPClient) prove(ctx context.Context, fleet, token string) error {
	nonce := rand.Text()
	u := c.base() + "/" + fleet + "/proof?nonce=" + url.QueryEscape(nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := c.client().Do(req)
	if err != nil {
		return c.noBoard(fleet)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil || res.StatusCode != http.StatusOK || res.Request.URL.String() != u {
		if err == nil && (res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusInternalServerError) {
			return refusedProof{c.noBoard(fleet)}
		}
		return c.noBoard(fleet)
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(nonce))
	if !hmac.Equal([]byte(strings.TrimSpace(string(body))), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return c.noBoard(fleet)
	}
	return nil
}

type refusedProof struct{ error }

func (c *HTTPClient) noBoard(fleet string) error {
	return fmt.Errorf("no board for fleet %s answers at %s; start it with `hand open` in that fleet", fleet, c.base())
}

func (c *HTTPClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// client never follows a redirect and never uses a cookie jar: the proof and
// the cookie belong to the one address the fleet recorded.
func (c *HTTPClient) client() *http.Client {
	client := *c.http()
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

func (c *HTTPClient) unanswered(err error) error {
	if c.Lost != nil {
		if lost := c.Lost(); lost != nil {
			return lost
		}
	}
	return fmt.Errorf("the board at %s did not answer: %w", c.base(), err)
}

func (c *HTTPClient) failure(res *http.Response) error {
	if c.Paired && res.StatusCode == http.StatusUnauthorized {
		return &APIError{Status: http.StatusForbidden, Code: CodeNotRecognised, Remote: true, Message: "the host does not recognise this device; pair it again"}
	}
	if !c.Paired || res.StatusCode != http.StatusForbidden {
		return remoteError(res)
	}
	if res.Header.Get("X-Hand-Error") != "" {
		return &APIError{Status: res.StatusCode, Code: CodeRevoked, Remote: true, Message: "revoked on the host; pair this device again or forget the host"}
	}
	err := remoteError(res)
	var api *APIError
	if errors.As(err, &api) {
		api.Remote = true
		return err
	}
	return &APIError{Status: res.StatusCode, Code: CodeNotRecognised, Remote: true, Message: "the host does not recognise this device; pair it again"}
}

func remoteError(res *http.Response) error {
	var body APIErrorResponse
	dec := json.NewDecoder(io.LimitReader(res.Body, 1<<20))
	if err := dec.Decode(&body); err == nil && body.Error.Code != "" {
		body.Error.Status = res.StatusCode
		return &body.Error
	}
	return fmt.Errorf("the board answered %s", res.Status)
}
