package board

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/state"
)

const (
	apiVersion = 1
	apiPrefix  = "/api/v1"
)

var supervisorRef = regexp.MustCompile(`^s[1-9][0-9]*$`)

type APIFleet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type APIEnvelope struct {
	Version     int      `json:"version"`
	Fleet       APIFleet `json:"fleet"`
	Cursor      string   `json:"cursor"`
	GeneratedAt string   `json:"generated_at"`
	Data        any      `json:"data"`
}

type APIError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type APIErrorResponse struct {
	Error APIError `json:"error"`
}

type APISession struct {
	CSRF string `json:"csrf"`
}

type APIGauge struct {
	Text  string `json:"text"`
	Level string `json:"level"`
	Pct   int    `json:"pct"`
}

type APISupervisor struct {
	Ref       string   `json:"ref"`
	Harness   string   `json:"harness"`
	Model     string   `json:"model,omitempty"`
	Effort    string   `json:"effort,omitempty"`
	Status    string   `json:"status"`
	Live      bool     `json:"live"`
	Pending   int      `json:"pending"`
	Resumable bool     `json:"resumable"`
	Blocked   bool     `json:"blocked,omitempty"`
	Keys      *APIKeys `json:"keys,omitempty"`
	Ask       *APIAsk  `json:"ask,omitempty"`
	Held      bool     `json:"held,omitempty"`
	Switching string   `json:"switching,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

type APIKeys struct {
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
	Pressed  int64  `json:"pressed"`
}

type APIAsk struct {
	Question string         `json:"question"`
	Options  []APIAskOption `json:"options"`
}

type APIAskOption struct {
	Label string   `json:"label"`
	Keys  []string `json:"keys"`
}

type APITaskCounts struct {
	Active    int `json:"active"`
	Inbox     int `json:"inbox"`
	Done      int `json:"done"`
	Abandoned int `json:"abandoned"`
}

type APINeed struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref,omitempty"`
	Title     string `json:"title,omitempty"`
	Word      string `json:"word"`
	Tone      string `json:"tone"`
	Task      string `json:"task,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Report    string `json:"report,omitempty"`
	Attempt   string `json:"attempt,omitempty"`
	Headline  string `json:"headline,omitempty"`
	Status    string `json:"status,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
	Cut       bool   `json:"cut,omitempty"`
}

type APINeeds struct {
	Waiting    int       `json:"waiting"`
	More       int       `json:"more"`
	Worst      string    `json:"worst,omitempty"`
	WorstWord  string    `json:"worst_word,omitempty"`
	WorstCount int       `json:"worst_count"`
	Cleared    string    `json:"cleared,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Items      []APINeed `json:"items"`
}

type APIStatus struct {
	Supervisor   *APISupervisor `json:"supervisor,omitempty"`
	AgentState   string         `json:"agent_state"`
	ComposerHint string         `json:"composer_hint"`
	Pill         string         `json:"pill"`
	Line         string         `json:"line"`
	Gauge        APIGauge       `json:"gauge"`
	Compactions  int            `json:"compactions"`
	Needs        APINeeds       `json:"needs"`
	Tasks        APITaskCounts  `json:"tasks"`
	Projects     int            `json:"projects"`
}

type APIShot struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Size    string `json:"size,omitempty"`
	Href    string `json:"href,omitempty"`
	Image   bool   `json:"image,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

type APIEntry struct {
	Role      string     `json:"role"`
	At        string     `json:"at,omitempty"`
	Text      string     `json:"text,omitempty"`
	No        int        `json:"no,omitempty"`
	Ref       string     `json:"ref,omitempty"`
	Model     string     `json:"model,omitempty"`
	Delivery  string     `json:"delivery,omitempty"`
	Delivered string     `json:"delivered,omitempty"`
	Input     int64      `json:"input,omitempty"`
	Queued    bool       `json:"queued,omitempty"`
	Wakes     []APIEntry `json:"wakes,omitempty"`
	Shots     []APIShot  `json:"shots,omitempty"`
}

type APIWorking struct {
	Ref   string `json:"ref"`
	Since string `json:"since"`
	On    string `json:"on"`
	From  string `json:"from,omitempty"`
	Last  string `json:"last,omitempty"`
	Input int64  `json:"input,omitempty"`
}

type APITimeline struct {
	Live    bool        `json:"live"`
	Older   int         `json:"older"`
	Note    string      `json:"note,omitempty"`
	Working *APIWorking `json:"working,omitempty"`
	Entries []APIEntry  `json:"entries"`
}

type APIDeliverable struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"`
	Href string `json:"href,omitempty"`
}

type APIWaits struct {
	Ref    string `json:"ref"`
	Due    string `json:"due"`
	Every  string `json:"every,omitempty"`
	Note   string `json:"note,omitempty"`
	Status string `json:"status"`
	Waits  bool   `json:"waits"`
}

type APIAttempt struct {
	Ref       string `json:"ref"`
	Task      string `json:"task"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Continues string `json:"continues,omitempty"`
	CreatedAt string `json:"created_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	CleanedAt string `json:"cleaned_at,omitempty"`
	Kept      string `json:"kept,omitempty"`
}

type APITaskRow struct {
	Ref        string          `json:"ref"`
	Title      string          `json:"title"`
	Project    string          `json:"project"`
	Status     string          `json:"status"`
	State      string          `json:"state"`
	Agent      string          `json:"agent,omitempty"`
	Attempt    *APIAttempt     `json:"attempt,omitempty"`
	Delivered  *APIDeliverable `json:"delivered,omitempty"`
	Waits      *APIWaits       `json:"waits,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
	Parent     string          `json:"parent,omitempty"`
	Children   int             `json:"children,omitempty"`
}

type APIAttemptRow struct {
	APIAttempt
	State string `json:"state"`
}

type APITaskGroup struct {
	Name  string       `json:"name"`
	Count int          `json:"count"`
	Tasks []APITaskRow `json:"tasks"`
}

type APITasks struct {
	All           bool           `json:"all"`
	Hidden        int            `json:"hidden"`
	FinishedCount int            `json:"finished_count"`
	Groups        []APITaskGroup `json:"groups"`
}

type APITask struct {
	Ref        string `json:"ref"`
	Title      string `json:"title"`
	Goal       string `json:"goal,omitempty"`
	Project    string `json:"project"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Parent     string `json:"parent,omitempty"`
}

type APIPlan struct {
	Ref       string `json:"ref"`
	Revision  int    `json:"revision"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type APIDecision struct {
	Ref        string `json:"ref"`
	Task       string `json:"task"`
	Status     string `json:"status"`
	Pill       string `json:"pill,omitempty"`
	Headline   string `json:"headline"`
	Question   string `json:"question"`
	Answer     string `json:"answer,omitempty"`
	AnsweredBy string `json:"answered_by,omitempty"`
	CreatedAt  string `json:"created_at"`
	ClosedAt   string `json:"closed_at,omitempty"`
}

type APIReport struct {
	Ref       string `json:"ref"`
	Task      string `json:"task"`
	Attempt   string `json:"attempt"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	AckedAt   string `json:"acked_at,omitempty"`
	AckedBy   string `json:"acked_by,omitempty"`
}

type APIEvent struct {
	Seq    int64  `json:"seq"`
	At     string `json:"at"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	Task   string `json:"task,omitempty"`
}

type APITaskDetail struct {
	Task          APITask          `json:"task"`
	Pill          string           `json:"pill"`
	Plan          *APIPlan         `json:"plan,omitempty"`
	Delivered     []APIDeliverable `json:"delivered"`
	Waits         *APIWaits        `json:"waits,omitempty"`
	OpenDecisions []APIDecision    `json:"open_decisions"`
	Decisions     []APIDecision    `json:"decisions"`
	Unread        []APIReport      `json:"unread"`
	Reports       []APIReport      `json:"reports"`
	MoreReports   bool             `json:"more_reports"`
	Attempts      []APIAttemptRow  `json:"attempts"`
	Events        []APIEvent       `json:"events"`
	MoreEvents    bool             `json:"more_events"`
	Children      []APITaskRow     `json:"children,omitempty"`
}

type APITurn struct {
	Role   string `json:"role"`
	Text   string `json:"text"`
	At     string `json:"at"`
	Queued bool   `json:"queued,omitempty"`
}

type APITranscript struct {
	Shown    bool      `json:"shown"`
	More     bool      `json:"more"`
	Note     string    `json:"note,omitempty"`
	Model    string    `json:"model,omitempty"`
	Answered string    `json:"answered,omitempty"`
	Turns    []APITurn `json:"turns"`
}

type APIAttemptDetail struct {
	Attempt     APIAttempt       `json:"attempt"`
	Task        APITask          `json:"task"`
	State       string           `json:"state"`
	Brief       string           `json:"brief,omitempty"`
	ModelNote   string           `json:"model_note,omitempty"`
	Duration    string           `json:"duration"`
	Reports     []APIReport      `json:"reports"`
	MoreReports bool             `json:"more_reports"`
	Events      []APIEvent       `json:"events"`
	MoreEvents  bool             `json:"more_events"`
	Delivered   []APIDeliverable `json:"delivered"`
	Transcript  APITranscript    `json:"transcript"`
}

type APIOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Note        string `json:"note,omitempty"`
	Value       string `json:"value"`
	Recommended bool   `json:"recommended,omitempty"`
	Other       bool   `json:"other,omitempty"`
	Long        bool   `json:"long,omitempty"`
}

type APIDecisionDetail struct {
	Decision APIDecision `json:"decision"`
	Task     APITask     `json:"task"`
	Body     string      `json:"body"`
	Options  []APIOption `json:"options"`
}

type APIReportDetail struct {
	Report    APIReport        `json:"report"`
	Task      APITask          `json:"task"`
	Delivered []APIDeliverable `json:"delivered"`
}

func apiPath(path string) bool {
	return path == apiPrefix || strings.HasPrefix(path, apiPrefix+"/")
}

func (b *Board) apiServe(w http.ResponseWriter, r *http.Request) {
	if !b.o.Controls || proxied(r) {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the API answers only on a loopback board or to a paired phone")
		return
	}
	s, fault := b.session(r)
	if fault != signedIn {
		b.apiFail(w, http.StatusUnauthorized, "unauthorized", "this request needs the board's session cookie")
		return
	}
	if peerOf(r) != nil && (s.Device == nil || !strings.HasPrefix(s.Device.Login, "iroh:")) {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the API answers on the peer listener only to a phone paired with its own key")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, s))
	markAuthed(w)
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		w.Header().Set("Allow", apiAllow(r))
		b.apiFail(w, http.StatusMethodNotAllowed, "method_not_allowed", "the v1 API takes GET and HEAD, and POST on its action paths")
		return
	}
	b.mux.ServeHTTP(w, r)
}

func apiActor(ctx context.Context) string {
	if s, _ := ctx.Value(sessionKey{}).(session); s.Device != nil {
		return "phone (" + s.Device.Name + ")"
	}
	return "operator (desktop)"
}

func apiAllow(r *http.Request) string {
	p := strings.TrimPrefix(r.URL.Path, apiPrefix)
	if p == "/message" || p == "/supervisor/start" || p == "/supervisor/resume" || p == "/supervisor/stop" ||
		p == "/supervisor/interrupt" || p == "/supervisor/force" || p == "/supervisor/keys" ||
		strings.HasPrefix(p, "/decision/") && strings.HasSuffix(p, "/answer") ||
		strings.HasPrefix(p, "/report/") && strings.HasSuffix(p, "/ack") {
		return "POST"
	}
	return "GET, HEAD"
}

func (b *Board) apiSession(w http.ResponseWriter, r *http.Request) {
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return
	}
	b.apiOK(w, f, since, APISession{CSRF: b.csrf(r.Context())})
}

func (b *Board) apiCSRF(w http.ResponseWriter, r *http.Request) bool {
	s, _ := r.Context().Value(sessionKey{}).(session)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Hand-CSRF")), []byte(s.CSRF)) != 1 {
		b.apiFail(w, http.StatusForbidden, "csrf_required", "the X-Hand-CSRF header is missing or wrong; read the session's token first")
		return false
	}
	return true
}

type apiAnswerBody struct {
	Choice string `json:"choice"`
	Note   string `json:"note"`
}

type apiMessageBody struct {
	Text json.RawMessage `json:"text"`
}

type apiKeysBody struct {
	Keys     []string `json:"keys"`
	Revision *int64   `json:"revision"`
	Digest   string   `json:"digest"`
	After    *int64   `json:"after"`
	Ref      string   `json:"ref"`
}

type apiControlBody struct {
	Ref string `json:"ref"`
}

// APIControlAccepted is a control's answer when it ran but the status could
// not be read back; the client warns and refreshes instead of reporting failure.
type APIControlAccepted struct {
	Accepted bool `json:"accepted"`
}

// APIMessageAccepted is the message mutation's answer when the board queued
// the text but could not read the conversation back.
type APIMessageAccepted struct {
	Accepted bool `json:"accepted"`
}

func (b *Board) apiAnswer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	id, err := pathID(r, "d")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object under 64 KiB")
		return
	}
	var in apiAnswerBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send a JSON object with choice and note")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with choice and note")
		return
	}
	choice, note := strings.TrimSpace(in.Choice), strings.TrimSpace(in.Note)
	answer := strings.TrimSpace(choice + "\n\n" + note)
	if answer == "" {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "pick an option or write an answer")
		return
	}
	if note == "" && b.asksForANote(ctx, id, choice) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "write your answer: that option asks for one")
		return
	}
	if _, err := b.st.Answer(ctx, id, answer, apiActor(ctx)); err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiMutationOK(w, ctx, func() (any, error) { return b.apiDecisionData(ctx, id) })
}

func (b *Board) apiAck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	id, err := pathID(r, "r")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.st.AckReport(ctx, id, apiActor(ctx)); err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiMutationOK(w, ctx, func() (any, error) { return b.apiReportData(ctx, id) })
}

func (b *Board) apiMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	if b.o.Control == nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the board's controls are off")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object under 64 KiB")
		return
	}
	var in apiMessageBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send a JSON object with text")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with text")
		return
	}
	text, err := apiMessageText(in.Text)
	if err != nil {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send a JSON object with text")
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if err := state.CheckMessage(text); err != nil {
		b.apiFailErr(w, err)
		return
	}
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.o.Control(ctx, "supervisor", "send", "--text", text); err != nil {
		b.apiFailErr(w, err)
		return
	}
	tl, err := b.apiTimelineData(ctx, url.Values{})
	if err != nil {
		b.apiOK(w, f, "", APIMessageAccepted{Accepted: true})
		return
	}
	b.apiOK(w, f, "", tl)
}

func (b *Board) apiControl(w http.ResponseWriter, r *http.Request, action string) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	if b.o.Control == nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the board's controls are off")
		return
	}
	args := []string{"supervisor", action}
	if action == "stop" || action == "interrupt" || action == "force" {
		ref, ok := b.apiControlRef(w, r)
		if !ok {
			return
		}
		args = append(args, "--expect", ref)
	}
	if action == "start" {
		if _, ok, err := b.st.LatestSupervisor(ctx); err != nil {
			b.apiFailErr(w, err)
			return
		} else if !ok {
			b.apiFail(w, http.StatusConflict, "conflict", "there is no earlier supervisor to copy; start the first one with `hand supervisor start`")
			return
		}
	}
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.o.Control(ctx, args...); err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiControlOK(w, f, ctx)
}

func (b *Board) apiControlRef(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with the supervisor the status showed")
		return "", false
	}
	var in apiControlBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with the supervisor the status showed")
		return "", false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with the supervisor the status showed")
		return "", false
	}
	if !supervisorRef.MatchString(in.Ref) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with the supervisor the status showed")
		return "", false
	}
	return in.Ref, true
}

func (b *Board) apiControlOK(w http.ResponseWriter, f state.Fleet, ctx context.Context) {
	out, err := b.apiStatusData(ctx)
	if err != nil {
		b.apiOK(w, f, "", APIControlAccepted{Accepted: true})
		return
	}
	b.apiOK(w, f, "", out)
}

func (b *Board) apiKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	if b.o.Control == nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the board's controls are off")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object under 64 KiB")
		return
	}
	var in apiKeysBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with keys, revision, digest, after and ref")
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with keys, revision, digest, after and ref")
		return
	}
	if len(in.Keys) == 0 || in.Revision == nil || *in.Revision < 0 || in.After == nil || *in.After < 0 || !digest.MatchString(in.Digest) || !supervisorRef.MatchString(in.Ref) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one JSON object with keys, revision, digest, after and ref")
		return
	}
	for _, k := range in.Keys {
		if !slices.Contains(state.SupervisorKeys, k) {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "use one of the board's supervisor keys")
			return
		}
	}
	args := []string{"supervisor", "keys", "--revision", strconv.FormatInt(*in.Revision, 10), "--screen", in.Digest, "--after", strconv.FormatInt(*in.After, 10), "--expect", in.Ref}
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.o.Control(ctx, append(args, in.Keys...)...); err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiControlOK(w, f, ctx)
}

// apiMessageText rejects the text a JSON decoder would quietly change: invalid
// UTF-8 bytes and unpaired surrogate escapes both become U+FFFD.
func apiMessageText(raw json.RawMessage) (string, error) {
	if len(raw) < 2 || raw[0] != '"' || !utf8.Valid(raw) || !surrogatesPaired(raw) {
		return "", errors.New("the text is not a JSON string the board can keep")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", err
	}
	return text, nil
}

func surrogatesPaired(raw []byte) bool {
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		v, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		switch {
		case v < 0xD800 || v > 0xDFFF:
			i += 6
		case v > 0xDBFF || i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u':
			return false
		default:
			lo, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || lo < 0xDC00 || lo > 0xDFFF {
				return false
			}
			i += 12
		}
	}
	return true
}

func (b *Board) apiMutationOK(w http.ResponseWriter, ctx context.Context, data func() (any, error)) {
	f, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	out, err := data()
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiOK(w, f, "", out)
}

func (b *Board) apiMethod(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	b.apiFail(w, http.StatusMethodNotAllowed, "method_not_allowed", "only an action path takes POST")
}

func (b *Board) apiStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiStatusData(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiTimeline(w http.ResponseWriter, r *http.Request) {
	ctx, q := r.Context(), r.URL.Query()
	if s := q.Get("before"); s != "" {
		if n, err := strconv.Atoi(s); err != nil || n < 0 {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "before must be a message number")
			return
		}
	}
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiTimelineData(ctx, q)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiTasksData(ctx, r.URL.Query())
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "t")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.st.Task(ctx, id); err != nil {
		b.apiFailErr(w, err)
		return
	}
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiTaskData(ctx, id)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiAttempt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "a")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.st.Attempt(ctx, id); err != nil {
		b.apiFailErr(w, err)
		return
	}
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return
	}
	data, err := b.apiAttemptData(ctx, id)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiOK(w, f, since, data)
}

func (b *Board) apiDecision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "d")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.st.Decision(ctx, id); err != nil {
		b.apiFailErr(w, err)
		return
	}
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiDecisionData(ctx, id)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "r")
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	if _, err := b.st.Report(ctx, id); err != nil {
		b.apiFailErr(w, err)
		return
	}
	f, cursor, ok := b.apiRead(w, r)
	if !ok {
		return
	}
	data, err := b.apiReportData(ctx, id)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiRespond(w, f, cursor, data)
}

func (b *Board) apiUnknown(w http.ResponseWriter, r *http.Request) {
	b.apiFail(w, http.StatusNotFound, "not_found", "there is no such API route")
}

func (b *Board) apiSince(w http.ResponseWriter, r *http.Request) (state.Fleet, string, bool) {
	f, err := b.st.Fleet(r.Context())
	if err != nil {
		b.apiFailErr(w, err)
		return state.Fleet{}, "", false
	}
	since := strings.TrimSpace(r.URL.Query().Get("since"))
	if since == "" {
		return f, "", true
	}
	if !b.apiDecodeCursor(f.ID, since) {
		b.apiFail(w, http.StatusBadRequest, "bad_cursor", "the change cursor is not one this board issued")
		return state.Fleet{}, "", false
	}
	return f, since, true
}

func (b *Board) apiOK(w http.ResponseWriter, f state.Fleet, since string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(f.Name + "\x00"))
	_, _ = sum.Write(raw)
	b.apiSend(w, f, since, b.apiEncodeCursor(f.ID, sum.Sum64()), raw)
}

func (b *Board) apiRead(w http.ResponseWriter, r *http.Request) (state.Fleet, string, bool) {
	f, since, ok := b.apiSince(w, r)
	if !ok {
		return f, "", false
	}
	v, err := b.sharedVersion(r.Context(), false)
	if err != nil {
		b.apiFailErr(w, err)
		return f, "", false
	}
	q := r.URL.Query()
	q.Del("since")
	sum := fnv.New64a()
	_, _ = fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%#v", f.Name, r.URL.Path, q.Encode(), v)
	cursor := b.apiEncodeCursor(f.ID, sum.Sum64())
	if since == cursor {
		w.Header().Set("X-Hand-Cursor", cursor)
		w.WriteHeader(http.StatusNotModified)
		return f, "", false
	}
	return f, cursor, true
}

func (b *Board) apiRespond(w http.ResponseWriter, f state.Fleet, cursor string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiSend(w, f, "", cursor, raw)
}

func (b *Board) apiSend(w http.ResponseWriter, f state.Fleet, since, cursor string, raw []byte) {
	w.Header().Set("X-Hand-Cursor", cursor)
	if since != "" && since == cursor {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(APIEnvelope{
		Version:     apiVersion,
		Fleet:       APIFleet{ID: f.ID, Name: f.Name},
		Cursor:      cursor,
		GeneratedAt: b.now().UTC().Format(time.RFC3339Nano),
		Data:        json.RawMessage(raw),
	})
}

func (b *Board) apiFail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIErrorResponse{Error: APIError{Status: status, Code: code, Message: Scrub(msg)}})
}

func (b *Board) apiFailErr(w http.ResponseWriter, err error) {
	status, msg := failure(err)
	b.apiFail(w, status, apiCode(status), msg)
}

func apiCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusInsufficientStorage:
		return "no_space"
	}
	return "internal"
}

func (b *Board) apiCursorTag(payload string) []byte {
	mac := hmac.New(sha256.New, []byte(b.token))
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)[:16]
}

func (b *Board) apiEncodeCursor(fleet string, sum uint64) string {
	payload := fleet + "\x00" + strconv.FormatUint(sum, 16)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(b.apiCursorTag(payload))
}

func (b *Board) apiDecodeCursor(fleet, cursor string) bool {
	head, tag, ok := strings.Cut(cursor, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(head)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != head {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(tag)
	if err != nil || base64.RawURLEncoding.EncodeToString(got) != tag || !hmac.Equal(got, b.apiCursorTag(string(raw))) {
		return false
	}
	id, text, ok := strings.Cut(string(raw), "\x00")
	if !ok || id != fleet {
		return false
	}
	n, err := strconv.ParseUint(text, 16, 64)
	return err == nil && strconv.FormatUint(n, 16) == text
}

func (b *Board) apiStatusData(ctx context.Context) (APIStatus, error) {
	f, err := b.facts(ctx)
	if err != nil {
		return APIStatus{}, err
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		return APIStatus{}, err
	}
	sv, err := b.buildSupervisor(ctx, url.Values{})
	if err != nil {
		return APIStatus{}, err
	}
	needs, err := b.buildNeeds(ctx, f, sv, viewContext{Controls: b.o.Controls, Base: b.o.Base, Multi: len(projects) > 1})
	if err != nil {
		return APIStatus{}, err
	}
	var pressed int64
	if sv.Sup != nil && sv.Blocked {
		pressed, err = b.st.LastKeys(ctx, state.SupervisorRef(sv.Sup.ID))
		if err != nil {
			return APIStatus{}, err
		}
	}
	status := b.buildStatus(ctx, sv)
	counts, err := b.st.CountTasks(ctx)
	if err != nil {
		return APIStatus{}, err
	}
	out := APIStatus{
		Supervisor:   apiSupervisor(sv, pressed),
		AgentState:   sv.AgentState,
		ComposerHint: sv.ComposerHint,
		Pill:         sv.Pill,
		Line:         sv.Line,
		Gauge:        APIGauge{Text: status.Gauge.Text, Level: status.Gauge.Level, Pct: status.Gauge.Pct},
		Compactions:  status.Compactions,
		Needs:        apiNeeds(needs),
		Tasks: APITaskCounts{
			Active:    counts[state.StatusActive],
			Inbox:     counts[state.StatusInbox],
			Done:      counts[state.StatusDone],
			Abandoned: counts[state.StatusAbandoned],
		},
		Projects: len(projects),
	}
	return out, nil
}

func (b *Board) apiTimelineData(ctx context.Context, q url.Values) (APITimeline, error) {
	sv, err := b.buildSupervisor(ctx, url.Values{})
	if err != nil {
		return APITimeline{}, err
	}
	v, err := b.buildTimeline(ctx, q, sv, viewContext{Controls: b.o.Controls, Base: b.o.Base}, true)
	if err != nil {
		return APITimeline{}, err
	}
	out := APITimeline{Live: v.Live, Older: v.Older, Note: v.Note, Entries: make([]APIEntry, 0, len(v.Entries))}
	if v.Working != nil {
		w := APIWorking{Ref: v.Working.Ref, Since: v.Working.Since, On: v.Working.On, From: v.Working.From, Last: v.Working.Last, Input: v.Working.Input}
		out.Working = &w
	}
	for _, e := range v.Entries {
		out.Entries = append(out.Entries, apiEntry(e))
	}
	return out, nil
}

func (b *Board) apiTasksData(ctx context.Context, q url.Values) (APITasks, error) {
	f, err := b.facts(ctx)
	if err != nil {
		return APITasks{}, err
	}
	projects, err := b.st.Projects(ctx)
	if err != nil {
		return APITasks{}, err
	}
	v, err := b.buildTasks(ctx, q, f, viewContext{Controls: b.o.Controls, Base: b.o.Base, Multi: len(projects) > 1}, true)
	if err != nil {
		return APITasks{}, err
	}
	out := APITasks{All: v.All, Hidden: v.Hidden, FinishedCount: v.FinishedCount, Groups: make([]APITaskGroup, 0, len(v.Groups))}
	for _, g := range v.Groups {
		group := APITaskGroup{Name: g.Name, Count: g.Count, Tasks: make([]APITaskRow, 0, len(g.Checks))}
		for _, c := range g.Checks {
			group.Tasks = append(group.Tasks, apiRow(c))
		}
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}

func (b *Board) apiTaskData(ctx context.Context, id int64) (APITaskDetail, error) {
	t, err := b.st.Task(ctx, id)
	if err != nil {
		return APITaskDetail{}, err
	}
	v, err := b.buildTask(ctx, t)
	if err != nil {
		return APITaskDetail{}, err
	}
	out := APITaskDetail{
		Task:          apiTask(t),
		Pill:          v.Pill,
		Delivered:     make([]APIDeliverable, 0, len(v.Card.Delivered)),
		OpenDecisions: make([]APIDecision, 0, len(v.Card.Decisions)),
		Decisions:     make([]APIDecision, 0, len(v.Decisions)),
		Unread:        make([]APIReport, 0, len(v.Unread)),
		Reports:       make([]APIReport, 0, len(v.Reports)),
		Attempts:      make([]APIAttemptRow, 0, len(v.Attempts)),
		Events:        make([]APIEvent, 0, len(v.Events)),
		MoreReports:   v.MoreReports,
		MoreEvents:    v.MoreEvents,
	}
	if v.Card.Plan != nil {
		p := apiPlan(*v.Card.Plan)
		out.Plan = &p
	}
	if v.Card.Waits != nil {
		w := apiWaits(*v.Card.Waits)
		out.Waits = &w
	}
	for _, d := range v.Card.Delivered {
		out.Delivered = append(out.Delivered, apiDeliverable(d))
	}
	for _, d := range v.Card.Decisions {
		out.OpenDecisions = append(out.OpenDecisions, apiDecision(d, decisionPill[d.Status]))
	}
	for _, d := range v.Decisions {
		out.Decisions = append(out.Decisions, apiDecision(d, decisionPill[d.Status]))
	}
	for _, r := range v.Unread {
		out.Unread = append(out.Unread, apiReport(r))
	}
	for _, r := range v.Reports {
		out.Reports = append(out.Reports, apiReport(r))
	}
	for _, c := range v.Attempts {
		out.Attempts = append(out.Attempts, apiAttemptRow(c))
	}
	for _, c := range v.Children {
		out.Children = append(out.Children, apiRow(c))
	}
	for _, e := range v.Events {
		out.Events = append(out.Events, apiEvent(e))
	}
	return out, nil
}

func (b *Board) apiAttemptData(ctx context.Context, id int64) (APIAttemptDetail, error) {
	v, err := b.buildAttempt(ctx, id)
	if err != nil {
		return APIAttemptDetail{}, err
	}
	out := APIAttemptDetail{
		Attempt:     apiAttempt(v.Attempt),
		Task:        apiTask(v.Task),
		State:       v.State,
		Brief:       v.Brief,
		ModelNote:   v.ModelNote,
		Duration:    v.Duration,
		Reports:     make([]APIReport, 0, len(v.Reports)),
		Events:      make([]APIEvent, 0, len(v.Events)),
		Delivered:   make([]APIDeliverable, 0, len(v.Delivered)),
		MoreReports: v.MoreReports,
		MoreEvents:  v.MoreEvents,
		Transcript:  apiTranscript(v.Transcript),
	}
	for _, r := range v.Reports {
		out.Reports = append(out.Reports, apiReport(r))
	}
	for _, e := range v.Events {
		out.Events = append(out.Events, apiEvent(e))
	}
	for _, d := range v.Delivered {
		out.Delivered = append(out.Delivered, apiDeliverable(d))
	}
	return out, nil
}

func (b *Board) apiDecisionData(ctx context.Context, id int64) (APIDecisionDetail, error) {
	v, err := b.buildDecision(ctx, id)
	if err != nil {
		return APIDecisionDetail{}, err
	}
	opts := options(v.Decision.Body())
	out := APIDecisionDetail{
		Decision: apiDecision(v.Decision, v.Pill),
		Task:     apiTask(v.Task),
		Body:     v.Decision.Body(),
		Options:  make([]APIOption, 0, len(opts)),
	}
	for _, o := range opts {
		out.Options = append(out.Options, APIOption{Key: o.Key, Label: o.Label, Note: o.Note, Value: o.Value, Recommended: o.Recommended, Other: o.Other, Long: o.Long})
	}
	return out, nil
}

func (b *Board) apiReportData(ctx context.Context, id int64) (APIReportDetail, error) {
	r, err := b.st.Report(ctx, id)
	if err != nil {
		return APIReportDetail{}, err
	}
	t, err := b.st.Task(ctx, r.TaskID)
	if err != nil {
		return APIReportDetail{}, err
	}
	out := APIReportDetail{Report: apiReport(r), Task: apiTask(t), Delivered: []APIDeliverable{}}
	for _, d := range b.linked(Deliverables(r.Body)) {
		out.Delivered = append(out.Delivered, apiDeliverable(d))
	}
	return out, nil
}

func apiSupervisor(v supervisorView, pressed int64) *APISupervisor {
	if v.Sup == nil {
		return nil
	}
	out := &APISupervisor{
		Ref:       v.Ref,
		Harness:   v.Sup.Harness,
		Model:     v.Sup.Model,
		Effort:    v.Sup.Effort,
		Status:    v.Sup.Status,
		Live:      v.Sup.Live(),
		Pending:   v.Pending,
		Resumable: v.Resumable,
		Blocked:   v.Blocked,
		Held:      v.Held,
		Reason:    Scrub(v.Sup.Reason),
	}
	if v.Blocked {
		out.Keys = &APIKeys{Revision: v.Revision, Digest: v.Digest, Pressed: pressed}
		if v.Stale == "" && v.ScreenNote == "" {
			out.Ask = apiAsk(ask(v.Screen, v.Agent))
		}
	}
	if out.Ref == "" {
		out.Ref = state.SupervisorRef(v.Sup.ID)
	}
	if v.Sup.Switching() {
		out.Switching = v.Sup.SwitchText()
	}
	return out
}

var askSecret = regexp.MustCompile(`(?i)\b(bearer|basic)\s+\S{6,}|\b(sk|pk|ghp|gho|ghs|xox[bap]|AKIA)[-_][A-Za-z0-9_-]{8,}|\b(api[_-]?key|token|secret|passw(or)?d|pwd|authorization)\b\s*[:=]|[A-Za-z0-9+/_-]{32,}`)

const (
	askQuestionMax = 200
	askLabelMax    = 60
	askOptionsMax  = 8
)

func askClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func apiAsk(card *askCard) *APIAsk {
	if card == nil || !strings.HasSuffix(card.Question, "?") || len(card.Options) > askOptionsMax || askSecret.MatchString(card.Question) {
		return nil
	}
	out := &APIAsk{Question: askClip(Scrub(card.Question), askQuestionMax), Options: make([]APIAskOption, 0, len(card.Options))}
	for _, o := range card.Options {
		keys := strings.Fields(o.Value)
		if len(keys) == 0 || askSecret.MatchString(o.Label) || slices.ContainsFunc(keys, func(k string) bool { return !slices.Contains(state.SupervisorKeys, k) }) {
			return nil
		}
		out.Options = append(out.Options, APIAskOption{Label: askClip(Scrub(o.Label), askLabelMax), Keys: keys})
	}
	return out
}

func apiNeeds(v needsView) APINeeds {
	out := APINeeds{
		Waiting:    v.Waiting,
		More:       v.More,
		Worst:      v.Worst,
		WorstWord:  v.WorstWord,
		WorstCount: v.WorstCount,
		Cleared:    v.Cleared,
		Summary:    v.Summary,
		Items:      make([]APINeed, 0, len(v.Waits)),
	}
	for _, w := range v.Waits {
		out.Items = append(out.Items, apiNeed(w))
	}
	return out
}

var idleFor = regexp.MustCompile(`^Supervisor idle for \S+ `)

func apiNeed(w waiting) APINeed {
	out := APINeed{Kind: w.Kind, Ref: w.Ref, Word: w.Word(), Tone: w.Tone()}
	switch w.Kind {
	case "blocked", "worker", "limited":
	case "stalled":
		out.Title = idleFor.ReplaceAllString(w.Title, "Supervisor idle ")
	default:
		out.Title = w.Title
	}
	if w.Task.ID != 0 {
		out.Task = state.TaskRef(w.Task.ID)
	}
	if w.Decision != nil {
		out.Decision = state.DecisionRef(w.Decision.ID)
		out.Headline = w.Decision.Headline()
		out.Status = w.Decision.Status
		out.CreatedAt = w.Decision.CreatedAt
	}
	if w.Report != nil {
		out.Report = state.ReportRef(w.Report.ID)
		out.Attempt = state.AttemptRef(w.Report.AttemptID)
		out.Status = w.Report.Status
		out.CreatedAt = w.Report.CreatedAt
		out.Excerpt, out.Cut = w.Excerpt, w.Cut
	} else if w.Attempt != nil {
		out.Attempt = state.AttemptRef(w.Attempt.ID)
	}
	return out
}

func apiWakeText(text string) string {
	const prefix = "wake: "
	if !strings.HasPrefix(text, prefix) {
		return text
	}
	parts := strings.Split(strings.TrimPrefix(text, prefix), "; ")
	out := make([]string, 0, len(parts))
	drop := false
	for _, line := range parts {
		kind := eventKind.FindString(line)
		if kind == "" || !strings.HasPrefix(line, kind) {
			if !drop {
				out = append(out, line)
			}
			continue
		}
		rest := strings.TrimLeft(strings.TrimPrefix(line, kind), " \t")
		end := strings.IndexAny(rest, " \t:")
		if end < 0 {
			end = len(rest)
		}
		ref := rest[:end]
		detail := strings.TrimLeft(rest[end:], " \t:")
		drop = screenKinds[kind] || strings.Contains(detail, "api error: ")
		if drop {
			out = append(out, strings.TrimSpace(kind+" "+ref))
		} else {
			out = append(out, line)
		}
	}
	return prefix + strings.Join(out, "; ")
}

func apiEntry(c chatItem) APIEntry {
	text := c.Text
	switch {
	case c.Role == "hand" && strings.HasPrefix(text, "wake: "):
		text = Scrub(apiWakeText(text))
	case c.Kind != "":
		if screenKinds[c.Kind] || strings.Contains(text, "api error: ") {
			text = ""
		} else {
			text = Scrub(text)
		}
	}
	out := APIEntry{
		Role:      c.Role,
		At:        c.At,
		Text:      text,
		No:        c.No,
		Ref:       c.Ref,
		Model:     c.Model,
		Delivery:  c.Delivery,
		Delivered: c.Delivered,
		Input:     c.Input,
		Queued:    c.Queued,
	}
	for _, w := range c.Wakes {
		out.Wakes = append(out.Wakes, apiEntry(w))
	}
	for _, s := range c.Shots {
		out.Shots = append(out.Shots, APIShot{Name: s.Name, Label: s.Label, Kind: s.Kind, Size: s.Size, Href: s.Href, Image: s.Image, Missing: s.Missing})
	}
	return out
}

func apiRow(c check) APITaskRow {
	out := APITaskRow{
		Ref:        state.TaskRef(c.Task.ID),
		Title:      c.Task.Title,
		Project:    c.Task.Project,
		Status:     c.Task.Status,
		State:      c.State,
		Agent:      c.Agent,
		FinishedAt: c.FinishedAt,
		Parent:     parentRef(c.Task),
		Children:   c.Subs,
	}
	if c.Attempt != nil {
		a := apiAttempt(*c.Attempt)
		out.Attempt = &a
	}
	if c.Delivered != nil {
		d := apiDeliverable(*c.Delivered)
		out.Delivered = &d
	}
	if c.Waits != nil {
		w := apiWaits(*c.Waits)
		out.Waits = &w
	}
	return out
}

func apiAttemptRow(c check) APIAttemptRow {
	out := APIAttemptRow{State: c.State}
	if c.Attempt != nil {
		out.APIAttempt = apiAttempt(*c.Attempt)
	}
	return out
}

func parentRef(t state.Task) string {
	if t.ParentID == 0 {
		return ""
	}
	return state.TaskRef(t.ParentID)
}

func apiTask(t state.Task) APITask {
	return APITask{
		Parent:     parentRef(t),
		Ref:        state.TaskRef(t.ID),
		Title:      t.Title,
		Goal:       t.Goal,
		Project:    t.Project,
		Status:     t.Status,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
		FinishedAt: t.FinishedAt,
	}
}

func apiAttempt(a state.Attempt) APIAttempt {
	out := APIAttempt{
		Ref:       state.AttemptRef(a.ID),
		Task:      state.TaskRef(a.TaskID),
		Harness:   a.Harness,
		Model:     a.Model,
		Effort:    a.Effort,
		Status:    a.Status,
		Reason:    Scrub(a.Reason),
		Branch:    a.Branch,
		CreatedAt: a.CreatedAt,
		EndedAt:   a.EndedAt,
		CleanedAt: a.CleanedAt,
		Kept:      a.Kept,
	}
	if a.Continues != 0 {
		out.Continues = state.AttemptRef(a.Continues)
	}
	return out
}

func apiDecision(d state.Decision, pill string) APIDecision {
	return APIDecision{
		Ref:        state.DecisionRef(d.ID),
		Task:       state.TaskRef(d.TaskID),
		Status:     d.Status,
		Pill:       pill,
		Headline:   d.Headline(),
		Question:   d.Question,
		Answer:     d.Answer,
		AnsweredBy: d.AnsweredBy,
		CreatedAt:  d.CreatedAt,
		ClosedAt:   d.ClosedAt,
	}
}

func apiReport(r state.Report) APIReport {
	return APIReport{
		Ref:       state.ReportRef(r.ID),
		Task:      state.TaskRef(r.TaskID),
		Attempt:   state.AttemptRef(r.AttemptID),
		Status:    r.Status,
		Summary:   r.Summary(),
		Body:      r.Body,
		CreatedAt: r.CreatedAt,
		AckedAt:   r.AckedAt,
		AckedBy:   r.AckedBy,
	}
}

var screenKinds = map[string]bool{
	"attempt.blocked":    true,
	"attempt.limited":    true,
	"supervisor.blocked": true,
	"supervisor.limited": true,
}

func apiEvent(e state.Event) APIEvent {
	out := APIEvent{Seq: e.Seq, At: e.At, Kind: e.Kind}
	if !screenKinds[e.Kind] && !strings.Contains(e.Detail, "api error: ") {
		out.Detail = Scrub(e.Detail)
	}
	if e.TaskID != 0 {
		out.Task = state.TaskRef(e.TaskID)
	}
	return out
}

func apiPlan(p state.Plan) APIPlan {
	return APIPlan{Ref: state.PlanRef(p.Revision), Revision: p.Revision, Body: p.Body, CreatedAt: p.CreatedAt}
}

func apiDeliverable(d Deliverable) APIDeliverable {
	if d.Kind == "file" {
		d.Ref = base(d.Ref)
	}
	return APIDeliverable{Ref: d.Ref, Kind: d.Kind, Href: d.Href}
}

func apiWaits(t state.Timer) APIWaits {
	out := APIWaits{Ref: state.TimerRef(t.ID), Due: t.Due.UTC().Format(time.RFC3339Nano), Note: t.Note, Status: t.Status, Waits: t.Waits}
	if t.Every > 0 {
		out.Every = t.Every.String()
	}
	return out
}

func apiTranscript(v workerView) APITranscript {
	out := APITranscript{
		Shown:    v.Shown,
		More:     v.More,
		Note:     v.Note,
		Model:    v.Model,
		Answered: v.Answered,
		Turns:    make([]APITurn, 0, len(v.Turns)),
	}
	for _, t := range v.Turns {
		out.Turns = append(out.Turns, APITurn{Role: t.Role, Text: t.Text, At: t.At, Queued: t.Queued})
	}
	return out
}
