package desktop

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ReadModel interface {
	Fleets() []Fleet
	Name(ctx context.Context, fleet string) (string, error)
	Status(ctx context.Context, fleet string) (Status, error)
	Timeline(ctx context.Context, fleet string, before int) (Timeline, error)
	Tasks(ctx context.Context, fleet string, all bool) (Tasks, error)
	Task(ctx context.Context, fleet, ref string) (TaskDetail, error)
	Attempt(ctx context.Context, fleet, ref string) (AttemptDetail, error)
	Decision(ctx context.Context, fleet, ref string) (DecisionDetail, error)
	Report(ctx context.Context, fleet, ref string) (ReportDetail, error)
}

const (
	apiVersion = 1
	maxBody    = 16 << 20
)

var (
	fleetIDShape = regexp.MustCompile(`^f[0-9a-f]{12}$`)
	// ErrNoBoard says why a window has nothing to read.
	ErrNoBoard = errors.New("no board is running; start it with `hand open` in a fleet")
)

type HTTPClient struct {
	Base  string
	Known []Fleet
	Token func(fleet string) (string, error)
	HTTP  *http.Client
}

func (c *HTTPClient) Fleets() []Fleet {
	return append([]Fleet(nil), c.Known...)
}

func NewLocal(getenv func(string) string) (*HTTPClient, error) {
	root, err := localRoot(getenv)
	if err != nil {
		return nil, err
	}
	base, err := localBoard(root)
	if err != nil {
		return nil, err
	}
	fleets, tokens, err := localFleets(root)
	if err != nil {
		return nil, err
	}
	return &HTTPClient{
		Base:  base,
		Known: fleets,
		Token: func(fleet string) (string, error) { return readBoardToken(tokens[fleet]) },
		HTTP:  &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func localRoot(getenv func(string) string) (string, error) {
	root := getenv("SECONDHAND_HOME")
	if root == "" {
		home := getenv("HOME")
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
	dir := filepath.Join(root, "fleets")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var fleets []Fleet
	tokens := map[string]string{}
	for _, entry := range entries {
		id := entry.Name()
		if !fleetIDShape.MatchString(id) {
			continue
		}
		home, err := fleetHome(dir, id)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(home, "hand.db")); err != nil || !info.Mode().IsRegular() {
			continue
		}
		token := filepath.Join(home, "board.token")
		tokens[id] = token
		fleets = append(fleets, Fleet{ID: id, Name: id})
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

func (c *HTTPClient) get(ctx context.Context, fleet, path string, out any) (Envelope, error) {
	if !fleetIDShape.MatchString(fleet) {
		return Envelope{}, fmt.Errorf("%q is not a fleet id", fleet)
	}
	token, err := c.Token(fleet)
	if err != nil {
		return Envelope{}, fmt.Errorf("fleet %s: %w", fleet, err)
	}
	if err := c.prove(ctx, fleet, token); err != nil {
		return Envelope{}, err
	}
	u := c.Base + "/" + fleet + "/api/v1" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Envelope{}, err
	}
	req.AddCookie(&http.Cookie{Name: "hand_board_" + fleet, Value: token})
	res, err := c.http().Do(req)
	if err != nil {
		return Envelope{}, fmt.Errorf("the board at %s did not answer: %v", c.Base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Envelope{}, remoteError(res)
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
	u := c.Base + "/" + fleet + "/proof?nonce=" + url.QueryEscape(nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	res, err := c.http().Do(req)
	if err != nil {
		return c.noBoard(fleet)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil || res.StatusCode != http.StatusOK {
		return c.noBoard(fleet)
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(nonce))
	if !hmac.Equal([]byte(strings.TrimSpace(string(body))), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return c.noBoard(fleet)
	}
	return nil
}

func (c *HTTPClient) noBoard(fleet string) error {
	return fmt.Errorf("no board for fleet %s answers at %s; start it with `hand open` in that fleet", fleet, c.Base)
}

func (c *HTTPClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
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
