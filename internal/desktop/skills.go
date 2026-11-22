package desktop

import (
	"context"
	"net/http"
	"time"
)

const skillsWait = 6 * time.Minute

type Skill struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Ref     string `json:"ref"`
	Commit  string `json:"commit"`
	Latest  string `json:"latest"`
	Managed bool   `json:"managed"`
	Trusted bool   `json:"trusted"`
	State   string `json:"state"`
	Detail  string `json:"detail"`
	Diff    string `json:"diff"`
	DiffCut bool   `json:"diff_cut"`
}

type SkillScope struct {
	Scope   string  `json:"scope"`
	Project string  `json:"project"`
	Error   string  `json:"error"`
	Skills  []Skill `json:"skills"`
}

type Skills struct {
	Scopes []SkillScope `json:"scopes"`
}

type SkillResult struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// SkillsRun is the answer to a sync, update or propose. Receipt and Warn come
// from the envelope.
type SkillsRun struct {
	Scope     string        `json:"scope"`
	Project   string        `json:"project"`
	Results   []SkillResult `json:"results"`
	Refused   int           `json:"refused"`
	Reconcile []string      `json:"reconcile"`
	Receipt   string        `json:"-"`
	Warn      string        `json:"-"`
}

type SkillsRequest struct {
	Scope   string `json:"scope"`
	Project string `json:"project,omitempty"`
	Name    string `json:"name,omitempty"`
	Yes     string `json:"yes,omitempty"`
}

type longWait struct{}

func (c *HTTPClient) Skills(ctx context.Context, fleet string) (Skills, error) {
	var out Skills
	_, err := c.get(context.WithValue(ctx, longWait{}, true), fleet, "/skills", &out)
	return out, err
}

func (c *HTTPClient) SkillsRun(ctx context.Context, fleet, verb string, req SkillsRequest) (SkillsRun, error) {
	var out SkillsRun
	env, err := c.post(context.WithValue(ctx, longWait{}, true), fleet, "/skills/"+verb, req, &out)
	out.Receipt, out.Warn = env.Receipt, env.Warn
	return out, err
}

func (c *HTTPClient) longClient(ctx context.Context) *http.Client {
	client := c.client()
	if ctx.Value(longWait{}) != nil {
		client.Timeout = 0
	}
	return client
}

func (r *Router) Skills(ctx context.Context, fleet string) (Skills, error) {
	return route(r, fleet, func(m ReadModel) (Skills, error) { return m.Skills(ctx, fleet) })
}

func (r *Router) SkillsRun(ctx context.Context, fleet, verb string, req SkillsRequest) (SkillsRun, error) {
	return route(r, fleet, func(m ReadModel) (SkillsRun, error) { return m.SkillsRun(ctx, fleet, verb, req) })
}
