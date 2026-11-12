package desktop

import "encoding/json"

type Fleet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Home string `json:"-"`
}

type Envelope struct {
	Version     int             `json:"version"`
	Fleet       Fleet           `json:"fleet"`
	Cursor      string          `json:"cursor"`
	GeneratedAt string          `json:"generated_at"`
	Data        json.RawMessage `json:"data"`
}

type APIError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Message }

type APIErrorResponse struct {
	Error APIError `json:"error"`
}

type Supervisor struct {
	Ref       string `json:"ref"`
	Harness   string `json:"harness"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Status    string `json:"status"`
	Live      bool   `json:"live"`
	Pending   int    `json:"pending"`
	Resumable bool   `json:"resumable"`
	Blocked   bool   `json:"blocked,omitempty"`
	Keys      *Keys  `json:"keys,omitempty"`
	Ask       *Ask   `json:"ask,omitempty"`
}

type Ask struct {
	Question string      `json:"question"`
	Options  []AskOption `json:"options"`
}

type AskOption struct {
	Label string   `json:"label"`
	Keys  []string `json:"keys"`
}

type Keys struct {
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
	Pressed  int64  `json:"pressed"`
}

type Gauge struct {
	Text  string `json:"text"`
	Level string `json:"level"`
	Pct   int    `json:"pct"`
}

type TaskCounts struct {
	Active    int `json:"active"`
	Inbox     int `json:"inbox"`
	Done      int `json:"done"`
	Abandoned int `json:"abandoned"`
}

type Need struct {
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

type Needs struct {
	Waiting    int    `json:"waiting"`
	More       int    `json:"more"`
	Worst      string `json:"worst,omitempty"`
	WorstWord  string `json:"worst_word,omitempty"`
	WorstCount int    `json:"worst_count"`
	Cleared    string `json:"cleared,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Items      []Need `json:"items"`
}

type Status struct {
	Supervisor   *Supervisor `json:"supervisor,omitempty"`
	AgentState   string      `json:"agent_state"`
	ComposerHint string      `json:"composer_hint"`
	Pill         string      `json:"pill"`
	Line         string      `json:"line"`
	Gauge        Gauge       `json:"gauge"`
	Compactions  int         `json:"compactions"`
	Needs        Needs       `json:"needs"`
	Tasks        TaskCounts  `json:"tasks"`
	Projects     int         `json:"projects"`
}

type Shot struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Size    string `json:"size,omitempty"`
	Href    string `json:"href,omitempty"`
	Image   bool   `json:"image,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

type Entry struct {
	Role      string  `json:"role"`
	At        string  `json:"at,omitempty"`
	Text      string  `json:"text,omitempty"`
	No        int     `json:"no,omitempty"`
	Ref       string  `json:"ref,omitempty"`
	Model     string  `json:"model,omitempty"`
	Delivery  string  `json:"delivery,omitempty"`
	Delivered string  `json:"delivered,omitempty"`
	Input     int64   `json:"input,omitempty"`
	Queued    bool    `json:"queued,omitempty"`
	Wakes     []Entry `json:"wakes,omitempty"`
	Shots     []Shot  `json:"shots,omitempty"`
}

type Working struct {
	Ref   string `json:"ref"`
	Since string `json:"since"`
	On    string `json:"on"`
	From  string `json:"from,omitempty"`
	Last  string `json:"last,omitempty"`
	Input int64  `json:"input,omitempty"`
}

type Timeline struct {
	Live    bool     `json:"live"`
	Older   int      `json:"older"`
	Note    string   `json:"note,omitempty"`
	Working *Working `json:"working,omitempty"`
	Entries []Entry  `json:"entries"`
}

type Deliverable struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"`
	Href string `json:"href,omitempty"`
}

type Waits struct {
	Ref    string `json:"ref"`
	Due    string `json:"due"`
	Every  string `json:"every,omitempty"`
	Note   string `json:"note,omitempty"`
	Status string `json:"status"`
	Waits  bool   `json:"waits"`
}

type Attempt struct {
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

type TaskRow struct {
	Ref        string       `json:"ref"`
	Title      string       `json:"title"`
	Project    string       `json:"project"`
	Status     string       `json:"status"`
	State      string       `json:"state"`
	Agent      string       `json:"agent,omitempty"`
	Attempt    *Attempt     `json:"attempt,omitempty"`
	Delivered  *Deliverable `json:"delivered,omitempty"`
	Waits      *Waits       `json:"waits,omitempty"`
	FinishedAt string       `json:"finished_at,omitempty"`
	Parent     string       `json:"parent,omitempty"`
}

type TaskGroup struct {
	Name  string    `json:"name"`
	Count int       `json:"count"`
	Tasks []TaskRow `json:"tasks"`
}

type Tasks struct {
	All           bool        `json:"all"`
	Hidden        int         `json:"hidden"`
	FinishedCount int         `json:"finished_count"`
	Groups        []TaskGroup `json:"groups"`
}

type Task struct {
	Ref        string `json:"ref"`
	Title      string `json:"title"`
	Goal       string `json:"goal,omitempty"`
	Project    string `json:"project"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

type Plan struct {
	Ref       string `json:"ref"`
	Revision  int    `json:"revision"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

type Decision struct {
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

type Report struct {
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

type Event struct {
	Seq    int64  `json:"seq"`
	At     string `json:"at"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	Task   string `json:"task,omitempty"`
}

type AttemptRow struct {
	Attempt
	State string `json:"state"`
}

type TaskDetail struct {
	Task          Task          `json:"task"`
	Pill          string        `json:"pill"`
	Plan          *Plan         `json:"plan,omitempty"`
	Delivered     []Deliverable `json:"delivered"`
	Waits         *Waits        `json:"waits,omitempty"`
	OpenDecisions []Decision    `json:"open_decisions"`
	Decisions     []Decision    `json:"decisions"`
	Unread        []Report      `json:"unread"`
	Reports       []Report      `json:"reports"`
	MoreReports   bool          `json:"more_reports"`
	Attempts      []AttemptRow  `json:"attempts"`
	Events        []Event       `json:"events"`
	MoreEvents    bool          `json:"more_events"`
}

type Turn struct {
	Role   string `json:"role"`
	Text   string `json:"text"`
	At     string `json:"at"`
	Queued bool   `json:"queued,omitempty"`
}

type Transcript struct {
	Shown    bool   `json:"shown"`
	More     bool   `json:"more"`
	Note     string `json:"note,omitempty"`
	Model    string `json:"model,omitempty"`
	Answered string `json:"answered,omitempty"`
	Turns    []Turn `json:"turns"`
}

type AttemptDetail struct {
	Attempt     Attempt       `json:"attempt"`
	Task        Task          `json:"task"`
	State       string        `json:"state"`
	Brief       string        `json:"brief,omitempty"`
	ModelNote   string        `json:"model_note,omitempty"`
	Duration    string        `json:"duration"`
	Reports     []Report      `json:"reports"`
	MoreReports bool          `json:"more_reports"`
	Events      []Event       `json:"events"`
	MoreEvents  bool          `json:"more_events"`
	Delivered   []Deliverable `json:"delivered"`
	Transcript  Transcript    `json:"transcript"`
}

type Option struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Note        string `json:"note,omitempty"`
	Value       string `json:"value"`
	Recommended bool   `json:"recommended,omitempty"`
	Other       bool   `json:"other,omitempty"`
	Long        bool   `json:"long,omitempty"`
}

type DecisionDetail struct {
	Decision Decision `json:"decision"`
	Task     Task     `json:"task"`
	Body     string   `json:"body"`
	Options  []Option `json:"options"`
}

type ReportDetail struct {
	Report    Report        `json:"report"`
	Task      Task          `json:"task"`
	Delivered []Deliverable `json:"delivered"`
}
