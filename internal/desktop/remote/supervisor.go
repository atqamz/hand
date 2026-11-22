package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/sidecar"
)

const (
	backoffMin = time.Second
	backoffMax = 30 * time.Second
	stableRun  = 10 * time.Second
)

var (
	ErrNoStore = errors.New("a supervisor needs a store and a hand-iroh resolver")
	ErrClosed  = errors.New("the supervisor is closed")
)

func fatal(code int) bool { return code == 2 || code == 3 }

type SupervisorOptions struct {
	Store    *Store
	Resolve  func(context.Context) (string, error)
	OnChange func(host string, st sidecar.ClientStatus)
	Min, Max time.Duration
}

type Supervisor struct {
	o    SupervisorOptions
	ctx  context.Context
	stop context.CancelFunc
	mu   sync.Mutex
	runs map[string]*run

	going   map[*run]struct{}
	qmu     sync.Mutex
	queue   []event
	kick    chan struct{}
	fin     chan struct{}
	finish  sync.Once
	drained chan struct{}

	stable time.Duration
	wait   func(context.Context, time.Duration) bool
}

type event struct {
	host string
	st   sidecar.ClientStatus
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
	st     sidecar.ClientStatus
}

func NewSupervisor(o SupervisorOptions) (*Supervisor, error) {
	if o.Store == nil || o.Resolve == nil {
		return nil, ErrNoStore
	}
	if o.Min <= 0 {
		o.Min = backoffMin
	}
	if o.Max < o.Min {
		o.Max = max(backoffMax, o.Min)
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Supervisor{o: o, ctx: ctx, stop: stop, runs: map[string]*run{}, going: map[*run]struct{}{}, kick: make(chan struct{}, 1), fin: make(chan struct{}), drained: make(chan struct{}), stable: stableRun, wait: sleep}
	if o.OnChange != nil {
		go s.dispatch()
	} else {
		close(s.drained)
	}
	return s, nil
}

func (s *Supervisor) Start(h Host, newKey bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return ErrClosed
	}
	dir, err := s.o.Store.KeyDir(h.ID)
	if err != nil {
		return err
	}
	if r := s.runs[h.ID]; r != nil {
		select {
		case <-r.done:
		default:
			return nil
		}
	}
	if _, err := sidecar.NewClient(s.options(h, dir, newKey)); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	r := &run{cancel: cancel, done: make(chan struct{}), st: sidecar.ClientStatus{State: sidecar.Starting}}
	s.runs[h.ID] = r
	go s.loop(ctx, r, h, dir, newKey)
	return nil
}

func (s *Supervisor) options(h Host, dir string, newKey bool) sidecar.ClientOptions {
	return sidecar.ClientOptions{
		Resolve: s.o.Resolve,
		Host:    h.ID,
		KeyDir:  dir,
		Dir:     dir,
		NewKey:  newKey,
		Relay:   h.Relay,
		OnChange: func(st sidecar.ClientStatus) {
			s.record(h.ID, st)
		},
	}
}

func (s *Supervisor) record(id string, st sidecar.ClientStatus) {
	s.mu.Lock()
	if r := s.runs[id]; r != nil {
		r.st = st
	}
	s.mu.Unlock()
	if s.o.OnChange == nil {
		return
	}
	s.qmu.Lock()
	s.queue = append(s.queue, event{id, st})
	s.qmu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Supervisor) dispatch() {
	defer close(s.drained)
	for {
		s.qmu.Lock()
		q := s.queue
		s.queue = nil
		s.qmu.Unlock()
		for _, e := range q {
			s.o.OnChange(e.host, e.st)
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-s.kick:
		case <-s.fin:
			s.qmu.Lock()
			empty := len(s.queue) == 0
			s.qmu.Unlock()
			if empty {
				return
			}
		}
	}
}

func (s *Supervisor) loop(ctx context.Context, r *run, h Host, dir string, newKey bool) {
	defer close(r.done)
	delay := s.o.Min
	for {
		_, err := os.Stat(filepath.Join(dir, keyFile))
		c, cerr := sidecar.NewClient(s.options(h, dir, newKey && err != nil))
		if cerr != nil {
			return
		}
		began := time.Now()
		c.Run(ctx)
		if ctx.Err() != nil || fatal(c.Status().Code) {
			return
		}
		if time.Since(began) >= s.stable {
			delay = s.o.Min
		}
		if !s.wait(ctx, delay) {
			return
		}
		delay = min(delay*2, s.o.Max)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *Supervisor) Status(host string) sidecar.ClientStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.runs[host]; r != nil {
		return r.st
	}
	return sidecar.ClientStatus{State: sidecar.Stopped}
}

func (s *Supervisor) Base(host string) string {
	if st := s.Status(host); st.Addr != "" {
		return "http://" + st.Addr
	}
	return ""
}

func (s *Supervisor) Stop(host string) {
	s.mu.Lock()
	r := s.runs[host]
	delete(s.runs, host)
	if r != nil {
		s.going[r] = struct{}{}
	}
	s.mu.Unlock()
	if r != nil {
		r.cancel()
		<-r.done
		s.mu.Lock()
		delete(s.going, r)
		s.mu.Unlock()
	}
}

func (s *Supervisor) Close() {
	s.stop()
	s.mu.Lock()
	runs := make([]*run, 0, len(s.runs)+len(s.going))
	for _, r := range s.runs {
		runs = append(runs, r)
	}
	for r := range s.going {
		runs = append(runs, r)
	}
	s.runs = map[string]*run{}
	s.mu.Unlock()
	for _, r := range runs {
		<-r.done
	}
	s.finish.Do(func() { close(s.fin) })
	<-s.drained
}
