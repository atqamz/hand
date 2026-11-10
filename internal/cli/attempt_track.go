package cli

import (
	"fmt"

	"github.com/atqamz/hand/internal/toon"
)

func cmdAttemptTrack(r *runner, args []string) error {
	if _, err := parse(flags("attempt track"), args, 0); err != nil {
		return err
	}
	if err := r.needHome(); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	tracks, err := st.TrackRecord(r.ctx())
	if err != nil {
		return err
	}
	track := make([][]string, 0, len(tracks))
	for _, t := range tracks {
		n := float64(t.N)
		median := ""
		if t.Timed {
			median = fmt.Sprintf("%.1f", t.Median)
		}
		track = append(track, []string{t.Harness, t.Model, t.Effort, fmt.Sprint(t.N),
			fmt.Sprintf("%.2f", float64(t.FirstTry)/n), fmt.Sprintf("%.2f", float64(t.Reattempt)/n), fmt.Sprintf("%.2f", float64(t.Stuck)/n),
			fmt.Sprintf("%.2f", float64(t.Sent)/n), fmt.Sprintf("%.2f", float64(t.Wakes)/n), median})
	}
	var d toon.Doc
	d.Rows("track", []string{"harness", "model", "effort", "n", "first_try", "reattempt", "stuck", "sent_per", "wakes_per", "median_min"}, track)
	return r.print(&d)
}
