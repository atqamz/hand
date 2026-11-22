package sidecar

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/proc"
)

const (
	Connecting  = "connecting"
	Connected   = "connected"
	Busy        = "busy"
	Unreachable = "unreachable"
	BoardDown   = "board_down"
	Lost        = "lost"
)

var (
	ErrClientOptions = errors.New("hand-iroh client options")

	hostID        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	relayValue    = regexp.MustCompile(`^(default|none|https?://[!-~]{1,200})$`)
	loopbackAddr  = regexp.MustCompile(`^127\.0\.0\.1:[0-9]{1,5}$`)
	listeningLine = regexp.MustCompile(`^listening addr=(127\.0\.0\.1:[0-9]{1,5}) id=([0-9a-f]{64})$`)
	linkLine      = regexp.MustCompile(`^state (connecting|connected|busy|unreachable|board_down|lost)$`)
)

type ClientStatus struct {
	State, Addr, ID, Link, Reason string
	Code                          int
}

type ClientOptions struct {
	Resolve   func(context.Context) (string, error)
	Host      string
	KeyDir    string
	Dir       string
	NewKey    bool
	Relay     string
	Direct    string
	Listen    string
	ReadyWait time.Duration
	StopWait  time.Duration
	OnChange  func(ClientStatus)
}

type Client struct {
	o  ClientOptions
	mu sync.Mutex
	st ClientStatus
}

func NewClient(o ClientOptions) (*Client, error) {
	switch {
	case o.Resolve == nil || o.KeyDir == "":
		return nil, fmt.Errorf("%w: a binary and a key folder are required", ErrClientOptions)
	case !hostID.MatchString(o.Host):
		return nil, fmt.Errorf("%w: the host is not an endpoint ID", ErrClientOptions)
	case o.Relay != "" && !relayValue.MatchString(o.Relay):
		return nil, fmt.Errorf("%w: the relay is not default, none or an http(s) URL", ErrClientOptions)
	case o.Listen != "" && !loopbackAddr.MatchString(o.Listen), o.Direct != "" && !loopbackAddr.MatchString(o.Direct):
		return nil, fmt.Errorf("%w: an address is not 127.0.0.1:PORT", ErrClientOptions)
	}
	if o.ReadyWait <= 0 {
		o.ReadyWait = 30 * time.Second
	}
	if o.StopWait <= 0 {
		o.StopWait = 5 * time.Second
	}
	return &Client{o: o}, nil
}

func (c *Client) Status() ClientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

func (c *Client) set(st ClientStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st = st
	if c.o.OnChange != nil {
		c.o.OnChange(st)
	}
}

func (c *Client) args() []string {
	o := c.o
	a := []string{"client", "--host", o.Host, "--key-dir", o.KeyDir}
	if o.NewKey {
		a = append(a, "--new-key")
	}
	for _, f := range [][2]string{{"--listen", o.Listen}, {"--relay", o.Relay}, {"--direct", o.Direct}} {
		if f[1] != "" {
			a = append(a, f[0], f[1])
		}
	}
	return a
}

func (c *Client) onLine(line string) {
	if m := listeningLine.FindStringSubmatch(line); m != nil {
		c.set(ClientStatus{State: Ready, Addr: m[1], ID: m[2]})
	} else if m := linkLine.FindStringSubmatch(line); m != nil {
		if st := c.Status(); st.State == Ready {
			st.Link = m[1]
			c.set(st)
		}
	}
}

func (c *Client) Run(ctx context.Context) {
	c.set(ClientStatus{State: Starting})
	bin, err := c.o.Resolve(ctx)
	if err != nil {
		c.finish(ctx, ClientStatus{State: Failed, Reason: clean(err.Error())})
		return
	}
	cmd := exec.Command(bin, c.args()...)
	cmd.Env, cmd.Dir, cmd.WaitDelay = []string{}, c.o.Dir, 2*time.Second
	proc.NoWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		c.finish(ctx, ClientStatus{State: Failed, Reason: clean("cannot start: " + err.Error())})
		return
	}
	errs := &tail{}
	cmd.Stdout, cmd.Stderr = &lines{on: c.onLine}, errs
	if err := cmd.Start(); err != nil {
		c.finish(ctx, ClientStatus{State: Failed, Reason: clean("cannot start: " + err.Error())})
		return
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	ready := time.NewTimer(c.o.ReadyWait)
	defer ready.Stop()
	var werr error
	for done := false; !done; {
		select {
		case werr = <-exited:
			done = true
		case <-ctx.Done():
			_ = stdin.Close()
			select {
			case werr = <-exited:
			case <-time.After(c.o.StopWait):
				_ = cmd.Process.Kill()
				werr = <-exited
			}
			done = true
		case <-ready.C:
			if c.Status().State != Ready {
				_ = cmd.Process.Kill()
				<-exited
				c.finish(ctx, ClientStatus{State: Failed, Code: -1, Reason: fmt.Sprintf("no listening line within %s", c.o.ReadyWait)})
				return
			}
		}
	}
	st := ClientStatus{State: Failed, Reason: "exit status 0"}
	var ee *exec.ExitError
	if errors.As(werr, &ee) {
		st.Code, st.Reason = ee.ExitCode(), werr.Error()
	} else if werr != nil {
		st.Code, st.Reason = -1, werr.Error()
	}
	if line := errs.last(); line != "" {
		st.Reason += ": " + line
	}
	st.Reason = clean(st.Reason)
	c.finish(ctx, st)
}

func (c *Client) finish(ctx context.Context, st ClientStatus) {
	if ctx.Err() != nil {
		st = ClientStatus{State: Stopped, Code: st.Code}
	}
	c.set(st)
}
