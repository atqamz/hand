package desktop_test

import (
	"context"
	"slices"

	"github.com/atqamz/hand/internal/desktop"
)

func (f *fixture) Features(ctx context.Context, id string) ([]string, error) {
	f.note("features")
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.features), f.featuresErr
}

func (f *fixture) Links(ctx context.Context, id string, paths []string) ([]*desktop.Link, error) {
	f.note("links")
	if f.linksGate != nil {
		f.linksEntered <- struct{}{}
		<-f.linksGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linkReqs = append(f.linkReqs, slices.Clone(paths))
	if f.linksErr != nil {
		return nil, f.linksErr
	}
	out := make([]*desktop.Link, len(paths))
	for i, p := range paths {
		if l, ok := f.linkTable[p]; ok && (f.linkOnly == "" || f.linkOnly == id) {
			out[i] = &l
		}
	}
	return out, nil
}

func (f *fixture) requests() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.linkReqs)
}
