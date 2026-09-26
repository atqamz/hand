package luvus_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func TestSubscribeStreamsEventsUntilTheServerDropsIt(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	srv.Publish("pane.created", map[string]any{"pane": "1"})
	stream, err := luvus.Client{Socket: srv.Socket}.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.Sequence != 1 {
		t.Fatalf("ack sequence = %d, want 1", stream.Sequence)
	}
	srv.Publish("pane.closed", map[string]any{"pane": "3"})
	ev, err := stream.Next()
	if err != nil || ev.Event != "pane.closed" || ev.Sequence != 2 || string(ev.Data) != `{"pane":"3"}` {
		t.Fatalf("next = %+v, %v", ev, err)
	}
	srv.DropSubscribers()
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after drop err = %v, want EOF", err)
	}
}

func TestSubscribeEndsWithItsContext(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := luvus.Client{Socket: srv.Socket}.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("next returned an event after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close when its context ended")
	}
}

func TestSubscribeReportsAnUnreachableServer(t *testing.T) {
	if _, err := (luvus.Client{Socket: sock(t)}).Subscribe(context.Background()); !errors.Is(err, luvus.ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
}
