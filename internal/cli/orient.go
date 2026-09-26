package cli

import (
	"context"

	"github.com/atqamz/hand/internal/orient"
)

func init() {
	commands["orient"] = cmdOrient
}

func cmdOrient(r *runner, args []string) error {
	if _, err := parse(flags("orient"), args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	doc, err := orient.Build(context.Background(), st, r.home, orient.DefaultBudget)
	if err != nil {
		return err
	}
	return r.print(doc)
}
