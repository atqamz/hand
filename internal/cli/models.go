package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

func init() {
	commands["models"] = cmdModels
}

func cmdModels(r *runner, args []string) error {
	fs := flags("models")
	refresh := fs.Bool("refresh", false, "run each harness's listing command now and rewrite its cache")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("models: %v", err)}
	}
	targets := harness.Listable
	switch fs.NArg() {
	case 0:
	case 1:
		if !slices.Contains(harness.Listable, fs.Arg(0)) {
			return fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, fs.Arg(0), strings.Join(harness.Listable, ", "))
		}
		targets = fs.Args()
	default:
		return usageError{fmt.Sprintf("models: want at most 1 argument after flags, got %d", fs.NArg())}
	}
	env, opts := r.modelOptions()
	if *refresh {
		if err := r.refreshModels(targets, env, opts); err != nil {
			return err
		}
	}
	now := r.env.Now()
	var rows, listings [][]string
	for _, name := range targets {
		st := harness.StatusOf(name, env, now)
		models, _ := harness.Detected(name, env)
		for _, m := range models {
			rows = append(rows, []string{name, m.Name, m.Label, effortsOf(m), string(m.Source)})
		}
		listings = append(listings, []string{name, st.Listing.Version, ageOf(st, now), listingState(name, st)})
	}
	var d toon.Doc
	d.Rows("models", []string{"harness", "model", "label", "efforts", "source"}, rows)
	d.Rows("listings", []string{"harness", "version", "age", "state"}, listings)
	if !*refresh {
		d.Help("Run each harness's listing command now: `hand models --refresh [HARNESS]`")
	}
	return r.print(&d)
}

func (r *runner) modelOptions() (harness.Env, harness.Options) {
	dir := r.home
	if dir == "" {
		dir, _ = r.getwd()
	}
	return harness.EnvOf(r.env.Getenv), harness.Options{Environ: r.env.Environ(), Dir: dir, Now: r.env.Now}
}

func (r *runner) refreshModels(names []string, env harness.Env, o harness.Options) error {
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { _, errs[i] = harness.Refresh(r.ctx(), name, env, o) })
	}
	wg.Wait()
	return errors.Join(errs...)
}

func effortsOf(m harness.Model) string {
	efforts := strings.Join(m.Efforts, " ")
	if m.Unverified {
		efforts += " (unverified)"
	}
	return efforts
}

func ageOf(st harness.Status, now time.Time) string {
	if st.Listing.At.IsZero() {
		return ""
	}
	switch d := now.Sub(st.Listing.At); {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	default:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
}

func listingState(name string, st harness.Status) string {
	failed := ""
	if st.Listing.Failed != nil {
		failed = st.Listing.Failed.Reason
	}
	switch {
	case st.Missing != "":
		return st.Missing
	case len(st.Listing.Models) == 0 && failed != "":
		return "no list: " + failed
	case len(st.Listing.Models) == 0:
		return "no list yet; run `hand models --refresh " + name + "`"
	}
	text := "ok"
	if st.Stale {
		text = "stale"
	}
	if failed != "" {
		text += "; refresh failed: " + failed
	}
	return text
}

func (r *runner) modelSummary(name string) string {
	env, now := harness.EnvOf(r.env.Getenv), r.env.Now()
	st := harness.StatusOf(name, env, now)
	models, _ := harness.Detected(name, env)
	if !st.Found || len(models) == 0 {
		return listingState(name, st)
	}
	sources := map[harness.Source]int{}
	for _, m := range models {
		sources[m.Source]++
	}
	var parts []string
	for _, s := range []harness.Source{harness.SourceBuiltin, harness.SourceDiscovered, harness.SourceCustom} {
		if n := sources[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", s, n))
		}
	}
	text := fmt.Sprintf("%d detected (%s), cache %s old", len(models), strings.Join(parts, ", "), ageOf(st, now))
	if tail := listingState(name, st); tail != "ok" {
		text += "; " + tail
	}
	return text
}
