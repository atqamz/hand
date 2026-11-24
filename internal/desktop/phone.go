package desktop

import (
	"context"
	"log/slog"
)

type Secret string

func (Secret) String() string       { return "[hidden]" }
func (Secret) GoString() string     { return `"[hidden]"` }
func (Secret) LogValue() slog.Value { return slog.StringValue("[hidden]") }
func (s Secret) Reveal() string     { return string(s) }

func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[hidden]"`), nil }

type PhoneIroh struct {
	State string `json:"state"`
	Tone  string `json:"tone"`
	Text  string `json:"text"`
	ID    string `json:"id,omitempty"`
}

type PhoneFleet struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Current  bool   `json:"current,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

type PhoneAt struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current,omitempty"`
	Expired bool   `json:"expired,omitempty"`
}

type PhoneDevice struct {
	ID       int64     `json:"id,omitempty"`
	Phone    string    `json:"phone,omitempty"`
	Name     string    `json:"name"`
	Login    string    `json:"login,omitempty"`
	PairedAt string    `json:"paired_at"`
	SeenAt   string    `json:"seen_at,omitempty"`
	Expired  bool      `json:"expired,omitempty"`
	Fleets   []PhoneAt `json:"fleets,omitempty"`
}

type PhoneFleetRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PhoneCode struct {
	Code      Secret          `json:"code"`
	Link      Secret          `json:"link"`
	URL       string          `json:"url"`
	Address   string          `json:"address"`
	PlainHTTP bool            `json:"plain_http,omitempty"`
	Fleets    []PhoneFleetRef `json:"fleets"`
	ExpiresAt string          `json:"expires_at"`
}

type PhoneApp struct {
	Link      Secret `json:"link"`
	ExpiresAt string `json:"expires_at"`
}

type PhoneFailed struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type PhoneRevoked struct {
	Revoked []PhoneFleetRef `json:"revoked"`
	Failed  []PhoneFailed   `json:"failed"`
}

type Phone struct {
	Iroh        *PhoneIroh    `json:"iroh,omitempty"`
	Fleets      []PhoneFleet  `json:"fleets"`
	Code        *PhoneCode    `json:"code,omitempty"`
	App         *PhoneApp     `json:"app,omitempty"`
	Devices     []PhoneDevice `json:"devices"`
	Unreachable []string      `json:"unreachable,omitempty"`
	Revoke      *PhoneRevoked `json:"revoke,omitempty"`
	Receipt     string        `json:"-"`
	Warn        string        `json:"-"`
}

type PhoneCodeRequest struct {
	Base   string   `json:"base"`
	Fleets []string `json:"fleets,omitempty"`
}

type PhoneRevokeRequest struct {
	Phone string `json:"phone,omitempty"`
	Fleet string `json:"fleet,omitempty"`
	ID    int64  `json:"id,omitempty"`
}

func (c *HTTPClient) Phone(ctx context.Context, fleet string) (Phone, error) {
	var out Phone
	_, err := c.get(ctx, fleet, "/phone", &out)
	return out, err
}

func (c *HTTPClient) PhoneCode(ctx context.Context, fleet string, req PhoneCodeRequest) (Phone, error) {
	return c.phoneAct(ctx, fleet, "/phone/code", req)
}

func (c *HTTPClient) PhoneApp(ctx context.Context, fleet string) (Phone, error) {
	return c.phoneAct(ctx, fleet, "/phone/app", nil)
}

func (c *HTTPClient) PhoneRevoke(ctx context.Context, fleet string, req PhoneRevokeRequest) (Phone, error) {
	return c.phoneAct(ctx, fleet, "/phone/revoke", req)
}

func (c *HTTPClient) phoneAct(ctx context.Context, fleet, path string, body any) (Phone, error) {
	var out Phone
	env, err := c.post(ctx, fleet, path, body, &out)
	out.Receipt, out.Warn = env.Receipt, env.Warn
	return out, err
}

func (r *Router) Phone(ctx context.Context, fleet string) (Phone, error) {
	return route(r, fleet, func(m ReadModel) (Phone, error) { return m.Phone(ctx, fleet) })
}

func (r *Router) PhoneCode(ctx context.Context, fleet string, req PhoneCodeRequest) (Phone, error) {
	return route(r, fleet, func(m ReadModel) (Phone, error) { return m.PhoneCode(ctx, fleet, req) })
}

func (r *Router) PhoneApp(ctx context.Context, fleet string) (Phone, error) {
	return route(r, fleet, func(m ReadModel) (Phone, error) { return m.PhoneApp(ctx, fleet) })
}

func (r *Router) PhoneRevoke(ctx context.Context, fleet string, req PhoneRevokeRequest) (Phone, error) {
	return route(r, fleet, func(m ReadModel) (Phone, error) { return m.PhoneRevoke(ctx, fleet, req) })
}
