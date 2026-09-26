package cli

import (
	"context"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var decisionCommands = map[string]handler{
	"ask":      cmdDecisionAsk,
	"answer":   cmdDecisionAnswer,
	"withdraw": cmdDecisionWithdraw,
	"list":     cmdDecisionList,
}

func init() {
	commands["decision"] = func(r *runner, args []string) error { return sub(r, args, "decision", decisionCommands) }
}

func decisionDoc(d state.Decision) *toon.Doc {
	var doc toon.Doc
	doc.Field("decision", state.DecisionRef(d.ID))
	doc.Field("task", state.TaskRef(d.TaskID))
	doc.Field("status", d.Status)
	return &doc
}

func cmdDecisionAsk(r *runner, args []string) error {
	pos, err := parse(flags("decision ask"), args, 2)
	if err != nil {
		return err
	}
	id, err := parseID("t", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.Ask(context.Background(), id, pos[1])
	if err != nil {
		return err
	}
	return r.print(decisionDoc(d))
}

func cmdDecisionAnswer(r *runner, args []string) error {
	fs := flags("decision answer")
	by := fs.String("by", "operator", "who answered")
	pos, err := parse(fs, args, 2)
	if err != nil {
		return err
	}
	id, err := parseID("d", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.Answer(context.Background(), id, pos[1], *by)
	if err != nil {
		return err
	}
	return r.print(decisionDoc(d))
}

func cmdDecisionWithdraw(r *runner, args []string) error {
	pos, err := parse(flags("decision withdraw"), args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("d", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.Withdraw(context.Background(), id)
	if err != nil {
		return err
	}
	return r.print(decisionDoc(d))
}

func cmdDecisionList(r *runner, args []string) error {
	fs := flags("decision list")
	limit := fs.Int("limit", 50, "maximum rows")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ds, err := st.OpenDecisions(context.Background(), 0, *limit)
	if err != nil {
		return err
	}
	var doc toon.Doc
	doc.Rows("decisions", []string{"id", "task", "question"}, decisionRows(ds))
	return r.print(&doc)
}

func decisionRows(ds []state.Decision) [][]string {
	rows := make([][]string, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, []string{state.DecisionRef(d.ID), state.TaskRef(d.TaskID), d.Question})
	}
	return rows
}
