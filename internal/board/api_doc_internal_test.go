package board

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCtxReaderStopsReadingOnceTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := ctxReader{ctx, strings.NewReader("abcdef")}
	buf := make([]byte, 3)
	if n, err := r.Read(buf); n != 3 || err != nil {
		t.Fatalf("live read = %d, %v", n, err)
	}
	cancel()
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel = %d, %v", n, err)
	}
}
