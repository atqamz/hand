package cmd

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "decision", Short: "Inspect and record exact canonical operator questions",
		PersistentPreRunE: canonicalSupervisorPreflight,
	}
	cmd.AddCommand(newDecisionShowCmd(), newDecisionListCmd(), newDecisionCreateCmd(), newDecisionAnswerCmd(), newDecisionDeliverCmd(), newDecisionCloseCmd())
	return cmd
}

func newDecisionListCmd() *cobra.Command {
	var after string
	var limit int
	cmd := &cobra.Command{
		Use: "list <task-id>", Short: "Read a bounded page of exact Decision history",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			page, err := store.ListCanonicalV19Decisions(cmd.Context(), homeDir, args[0], after, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(page.Items))
			for _, item := range page.Items {
				rows = append(rows, []string{item.ID, item.CreatedAt, item.ScopeKind, item.State})
			}
			var doc axi.Doc
			doc.Field("task_id", args[0])
			doc.Field("limit", strconv.Itoa(limit))
			doc.Field("next_after", page.NextAfter)
			doc.Rows("decisions", []string{"id", "created_at", "scope", "state"}, rows)
			doc.Help("Decision history includes archived Tasks. Pass next_after as --after for the next page; use decision show <id> for exact authority detail. State does not imply owner currentness or Worker delivery.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "Exclusive Decision ID cursor from the preceding page")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum Decisions per page (1–1000)")
	return cmd
}

func newDecisionShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <id>", Short: "Read one exact Decision and its authority history",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			view, err := store.ReadCanonicalV19Decision(cmd.Context(), homeDir, args[0])
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("decision_id", view.Decision.ID)
			doc.Field("task_id", view.Decision.TaskID)
			doc.Field("plan_id", valueOrNone(view.Decision.PlanID))
			doc.Field("attempt_id", valueOrNone(view.Decision.AttemptID))
			doc.Field("scope", view.Decision.ScopeKind)
			doc.Field("triggering_worker_report_id", valueOrNone(view.Decision.TriggeringWorkerReportID))
			doc.Field("question", view.Decision.Question)
			doc.Field("choices_digest", valueOrNone(view.Decision.ChoicesDigest))
			doc.Field("created_at", view.Decision.CreatedAt)
			doc.Bool("owner_current", view.OwnerCurrent)
			state := "open"
			if view.Answer != nil {
				state = "answered"
				doc.Field("answer_id", view.Answer.ID)
				doc.Field("answer", view.Answer.Answer)
				doc.Field("answer_digest", view.Answer.AnswerDigest)
				doc.Field("actor_kind", view.Answer.ActorKind)
				doc.Field("actor_ref", view.Answer.ActorRef)
				doc.Field("answered_at", view.Answer.AnsweredAt)
			}
			if view.Closure != nil {
				state = "closed"
				doc.Field("closure_reason", view.Closure.Reason)
				doc.Field("closed_at", view.Closure.ClosedAt)
				doc.Field("closure_evidence_digest", view.Closure.EvidenceDigest)
			}
			doc.Field("state", state)
			doc.Help("Answer authority is separate from WorkerInput, wake, acknowledgement and Task progression.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newDecisionCreateCmd() *cobra.Command {
	var input store.CanonicalV19DecisionCreateInput
	cmd := &cobra.Command{
		Use: "create <id>", Short: "Record one exact authority question",
		Long: "Retain the ID, timestamp and exact arguments for idempotent replay. Scope must identify the narrowest exact Task/Plan/Attempt owner; an optional report ID binds triggering evidence. This records a question, not an Answer or TaskHold.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.CreatedAt); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.ID = args[0]
			if err := store.CreateCanonicalV19Decision(cmd.Context(), homeDir, input); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("decision_id", input.ID)
			doc.Field("result", "recorded-or-already-recorded")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"task-id", "Exact canonical Task ID", &input.TaskID},
		{"scope", "task, plan or attempt; explicit exact owner scope", &input.ScopeKind},
		{"question", "Bounded immutable operator question", &input.Question},
		{"created-at", "Caller-retained RFC3339 timestamp for exact replay", &input.CreatedAt},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	cmd.Flags().StringVar(&input.PlanID, "plan-id", "", "Exact Plan ID for plan/attempt scope")
	cmd.Flags().StringVar(&input.AttemptID, "attempt-id", "", "Exact Attempt ID for attempt scope")
	cmd.Flags().StringVar(&input.TriggeringWorkerReportID, "report-id", "", "Exact triggering WorkerReport ID, when applicable")
	cmd.Flags().StringVar(&input.ChoicesDigest, "choices-digest", "", "Exact lowercase SHA-256 of externally retained choices, when applicable")
	return cmd
}

func newDecisionAnswerCmd() *cobra.Command {
	var input store.CanonicalV19DecisionAnswerCreateInput
	var explicitOperator bool
	cmd := &cobra.Command{
		Use: "answer <decision-id>", Short: "Record an explicit operator Answer for one exact Decision",
		Long: "Use only for an already-explicit operator answer, including one relayed by a Supervisor. --operator-answer declares that provenance; it is not authentication and must never be supplied for a recommendation, Worker Claim or provider observation. Retain the Answer ID, timestamp and exact bytes for replay. This records authority only; it does not send terminal input, wake or acknowledge a Worker, resolve a Hold, or advance work.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !explicitOperator || strings.TrimSpace(input.ActorRef) == "" {
				return fmt.Errorf("explicit --operator-answer and nonblank --operator-ref are required")
			}
			if err := canonicalTimestamp(input.AnsweredAt); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.DecisionID, input.ActorKind = args[0], "operator"
			input.AnswerDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(input.Answer)))
			if err := store.CreateCanonicalV19DecisionAnswer(cmd.Context(), homeDir, input); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("decision_id", input.DecisionID)
			doc.Field("answer_id", input.ID)
			doc.Field("answer_digest", input.AnswerDigest)
			doc.Field("result", "recorded-or-already-recorded")
			doc.Help("Authority recorded; Worker delivery and acknowledgement require separate evidence.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&explicitOperator, "operator-answer", false, "Declare an already-explicit operator answer")
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"answer-id", "Caller-retained immutable Answer ID", &input.ID},
		{"answer", "Exact explicit operator Answer bytes", &input.Answer},
		{"operator-ref", "Non-secret operator provenance reference; not authentication", &input.ActorRef},
		{"answered-at", "Caller-retained RFC3339 timestamp for exact replay", &input.AnsweredAt},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	return cmd
}

func newDecisionDeliverCmd() *cobra.Command {
	var input store.CanonicalV19AnswerWorkerInputCreateInput
	cmd := &cobra.Command{
		Use: "deliver <decision-id>", Short: "Record an exact Answer-origin WorkerInput",
		Long: "Copy one exact recorded Answer into an immutable WorkerInput for the named active Attempt and ExecutorBinding. Retain all IDs and the timestamp for replay. This does not wake or acknowledge the Worker.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.CreatedAt); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.DecisionID = args[0]
			view, err := store.ReadCanonicalV19Decision(cmd.Context(), homeDir, input.DecisionID)
			if err != nil {
				return err
			}
			if view.Answer == nil || view.Answer.ID != input.AnswerID {
				return fmt.Errorf("decision %q has no exact Answer %q", input.DecisionID, input.AnswerID)
			}
			input.Payload, input.PayloadDigest = view.Answer.Answer, view.Answer.AnswerDigest
			created, err := store.CreateCanonicalV19AnswerWorkerInput(cmd.Context(), homeDir, input)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("decision_id", input.DecisionID)
			doc.Field("answer_id", input.AnswerID)
			doc.Field("worker_input_id", created.ID)
			doc.Int("ordinal", int(created.Ordinal))
			doc.Field("result", "recorded-or-already-recorded")
			doc.Help("WorkerInput recorded; wake and acknowledgement require separate evidence.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"answer-id", "Exact recorded Answer ID", &input.AnswerID},
		{"input-id", "Caller-retained immutable WorkerInput ID", &input.ID},
		{"answer-origin-id", "Caller-retained exact Answer-origin binding ID", &input.AnswerOriginID},
		{"attempt-id", "Exact active Attempt ID", &input.AttemptID},
		{"executor-binding-id", "Exact open ExecutorBinding ID", &input.ExecutorBindingID},
		{"created-at", "Caller-retained RFC3339 timestamp for exact replay", &input.CreatedAt},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	return cmd
}

func newDecisionCloseCmd() *cobra.Command {
	var input store.CanonicalV19DecisionCloseInput
	cmd := &cobra.Command{
		Use: "close <decision-id>", Short: "Record exact stale or cancelled Decision closure",
		Long: "Close only the named unanswered Decision with retained evidence. Stale closure requires positively stale ownership in the same writer transaction; a failed observation is not proof. Retain the exact timestamp and evidence digest for replay. Closure never records an Answer, resolves a TaskHold or advances work.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.ClosedAt); err != nil {
				return err
			}
			if err := canonicalEvidenceDigest(input.EvidenceDigest); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.DecisionID = args[0]
			if err := store.CloseCanonicalV19Decision(cmd.Context(), homeDir, input); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("decision_id", input.DecisionID)
			doc.Field("closure_reason", input.Reason)
			doc.Field("closure_evidence_digest", input.EvidenceDigest)
			doc.Field("result", "recorded-or-already-recorded")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"reason", "stale or cancelled, justified by the owning question's semantics", &input.Reason},
		{"closed-at", "Caller-retained RFC3339 timestamp for exact replay", &input.ClosedAt},
		{"evidence-digest", "Exact lowercase SHA-256 of externally retained closure evidence", &input.EvidenceDigest},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	return cmd
}
