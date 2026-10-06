package cli

import (
	"context"
	"fmt"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var decisionCommands = map[string]handler{
	"ask":      cmdDecisionAsk,
	"answer":   cmdDecisionAnswer,
	"withdraw": cmdDecisionWithdraw,
	"list":     cmdDecisionList,
	"show":     cmdDecisionShow,
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
	fs := flags("decision ask")
	file := fs.String("file", "", "file holding the question, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("decision ask: %v", err)}
	}
	if fs.NArg() < 1 || (*file == "") != (fs.NArg() == 2) || fs.NArg() > 2 {
		return usageError{"usage: hand decision ask [--file PATH|-] tN [QUESTION]; give exactly one of --file or QUESTION"}
	}
	id, err := parseID("t", fs.Arg(0))
	if err != nil {
		return err
	}
	question := fs.Arg(1)
	if *file != "" {
		if question, err = r.readText(*file, 4*state.MaxQuestion+1); err != nil {
			return err
		}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := st.Ask(context.Background(), id, question)
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

func cmdDecisionShow(r *runner, args []string) error {
	pos, err := parse(flags("decision show"), args, 1)
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
	d, err := st.Decision(context.Background(), id)
	if err != nil {
		return err
	}
	doc := decisionDoc(d)
	doc.Field("question", d.Question)
	if d.Status == state.DecisionAnswered {
		doc.Field("answer", d.Answer)
		doc.Field("answered_by", d.AnsweredBy)
	}
	if d.ClosedAt != "" {
		doc.Field("closed_at", d.ClosedAt)
	}
	return r.print(doc)
}

func cmdDecisionList(r *runner, args []string) error {
	fs := flags("decision list")
	limit := fs.Int("limit", 50, "maximum rows")
	all := fs.Bool("all", false, "include answered and withdrawn decisions")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	var doc toon.Doc
	if *all {
		ds, err := st.Decisions(context.Background(), 0, *limit)
		if err != nil {
			return err
		}
		rows := make([][]string, 0, len(ds))
		for _, d := range ds {
			rows = append(rows, []string{state.DecisionRef(d.ID), state.TaskRef(d.TaskID), d.Status, d.Question})
		}
		doc.Rows("decisions", []string{"id", "task", "status", "question"}, rows)
		doc.Help("Read an answer: `hand decision show dN`")
		return r.print(&doc)
	}
	ds, err := st.OpenDecisions(context.Background(), 0, *limit)
	if err != nil {
		return err
	}
	doc.Rows("decisions", []string{"id", "task", "question"}, decisionRows(ds))
	doc.Help("Answered and withdrawn ones: `hand decision list --all`")
	return r.print(&doc)
}

func decisionRows(ds []state.Decision) [][]string {
	rows := make([][]string, 0, len(ds))
	for _, d := range ds {
		rows = append(rows, []string{state.DecisionRef(d.ID), state.TaskRef(d.TaskID), d.Question})
	}
	return rows
}
