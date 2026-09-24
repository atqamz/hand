CREATE TRIGGER artifact_no_replace
BEFORE INSERT ON artifact
WHEN EXISTS (SELECT 1 FROM artifact WHERE production_operation_id=NEW.production_operation_id)
  OR EXISTS (SELECT 1 FROM artifact WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'artifact is immutable');
END;

CREATE TRIGGER artifact_no_replace_update
BEFORE UPDATE ON artifact
WHEN EXISTS (SELECT 1 FROM artifact WHERE production_operation_id=NEW.production_operation_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM artifact WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'artifact is immutable');
END;

CREATE TRIGGER attempt_no_replace
BEFORE INSERT ON attempt
WHEN (NEW.lifecycle='active' AND EXISTS (SELECT 1 FROM attempt WHERE plan_id=NEW.plan_id AND lifecycle='active'))
  OR EXISTS (SELECT 1 FROM attempt WHERE plan_id=NEW.plan_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM attempt WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'attempt is immutable');
END;

CREATE TRIGGER attempt_no_replace_update
BEFORE UPDATE ON attempt
WHEN (NEW.lifecycle='active' AND EXISTS (SELECT 1 FROM attempt WHERE plan_id=NEW.plan_id AND NOT (id=OLD.id) AND lifecycle='active'))
  OR EXISTS (SELECT 1 FROM attempt WHERE plan_id=NEW.plan_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM attempt WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'attempt is immutable');
END;

CREATE TRIGGER attempt_backoff_no_replace
BEFORE INSERT ON attempt_backoff
WHEN EXISTS (SELECT 1 FROM attempt_backoff WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM attempt_backoff WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'attempt_backoff is immutable');
END;

CREATE TRIGGER attempt_backoff_no_replace_update
BEFORE UPDATE ON attempt_backoff
WHEN EXISTS (SELECT 1 FROM attempt_backoff WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM attempt_backoff WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'attempt_backoff is immutable');
END;

CREATE TRIGGER attempt_backoff_resolution_no_replace
BEFORE INSERT ON attempt_backoff_resolution
WHEN EXISTS (SELECT 1 FROM attempt_backoff_resolution WHERE backoff_id=NEW.backoff_id)
BEGIN
  SELECT RAISE(ABORT,'attempt_backoff_resolution is immutable');
END;

CREATE TRIGGER attempt_backoff_resolution_no_replace_update
BEFORE UPDATE ON attempt_backoff_resolution
WHEN EXISTS (SELECT 1 FROM attempt_backoff_resolution WHERE backoff_id=NEW.backoff_id AND NOT (backoff_id=OLD.backoff_id))
BEGIN
  SELECT RAISE(ABORT,'attempt_backoff_resolution is immutable');
END;

CREATE TRIGGER attempt_fallback_step_no_replace
BEFORE INSERT ON attempt_fallback_step
WHEN EXISTS (SELECT 1 FROM attempt_fallback_step WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal)
BEGIN
  SELECT RAISE(ABORT,'attempt_fallback_step is immutable');
END;

CREATE TRIGGER attempt_fallback_step_no_replace_update
BEFORE UPDATE ON attempt_fallback_step
WHEN EXISTS (SELECT 1 FROM attempt_fallback_step WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal AND NOT (attempt_id=OLD.attempt_id AND ordinal=OLD.ordinal))
BEGIN
  SELECT RAISE(ABORT,'attempt_fallback_step is immutable');
END;

CREATE TRIGGER attempt_worktree_binding_no_replace
BEFORE INSERT ON attempt_worktree_binding
WHEN EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE create_operation_id=NEW.create_operation_id)
  OR EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE attempt_id=NEW.attempt_id)
  OR EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'attempt_worktree_binding is immutable');
END;

CREATE TRIGGER attempt_worktree_binding_no_replace_update
BEFORE UPDATE ON attempt_worktree_binding
WHEN EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE create_operation_id=NEW.create_operation_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE attempt_id=NEW.attempt_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM attempt_worktree_binding WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'attempt_worktree_binding is immutable');
END;

CREATE TRIGGER decision_no_replace
BEFORE INSERT ON decision
WHEN EXISTS (SELECT 1 FROM decision WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'decision is immutable');
END;

CREATE TRIGGER decision_no_replace_update
BEFORE UPDATE ON decision
WHEN EXISTS (SELECT 1 FROM decision WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'decision is immutable');
END;

CREATE TRIGGER decision_answer_no_replace
BEFORE INSERT ON decision_answer
WHEN EXISTS (SELECT 1 FROM decision_answer WHERE decision_id=NEW.decision_id)
  OR EXISTS (SELECT 1 FROM decision_answer WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'decision_answer is immutable');
END;

CREATE TRIGGER decision_answer_no_replace_update
BEFORE UPDATE ON decision_answer
WHEN EXISTS (SELECT 1 FROM decision_answer WHERE decision_id=NEW.decision_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM decision_answer WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'decision_answer is immutable');
END;

CREATE TRIGGER decision_closure_no_replace
BEFORE INSERT ON decision_closure
WHEN EXISTS (SELECT 1 FROM decision_closure WHERE decision_id=NEW.decision_id)
BEGIN
  SELECT RAISE(ABORT,'decision_closure is immutable');
END;

CREATE TRIGGER decision_closure_no_replace_update
BEFORE UPDATE ON decision_closure
WHEN EXISTS (SELECT 1 FROM decision_closure WHERE decision_id=NEW.decision_id AND NOT (decision_id=OLD.decision_id))
BEGIN
  SELECT RAISE(ABORT,'decision_closure is immutable');
END;

CREATE TRIGGER executor_binding_no_replace
BEFORE INSERT ON executor_binding
WHEN EXISTS (SELECT 1 FROM executor_binding WHERE launch_operation_id=NEW.launch_operation_id)
  OR EXISTS (SELECT 1 FROM executor_binding WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'executor_binding is immutable');
END;

CREATE TRIGGER executor_binding_no_replace_update
BEFORE UPDATE ON executor_binding
WHEN EXISTS (SELECT 1 FROM executor_binding WHERE launch_operation_id=NEW.launch_operation_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM executor_binding WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'executor_binding is immutable');
END;

CREATE TRIGGER executor_binding_termination_no_replace
BEFORE INSERT ON executor_binding_termination
WHEN EXISTS (SELECT 1 FROM executor_binding_termination WHERE interrupt_operation_id=NEW.interrupt_operation_id)
  OR EXISTS (SELECT 1 FROM executor_binding_termination WHERE executor_binding_id=NEW.executor_binding_id)
BEGIN
  SELECT RAISE(ABORT,'executor_binding_termination is immutable');
END;

CREATE TRIGGER executor_binding_termination_no_replace_update
BEFORE UPDATE ON executor_binding_termination
WHEN EXISTS (SELECT 1 FROM executor_binding_termination WHERE interrupt_operation_id=NEW.interrupt_operation_id AND NOT (executor_binding_id=OLD.executor_binding_id))
  OR EXISTS (SELECT 1 FROM executor_binding_termination WHERE executor_binding_id=NEW.executor_binding_id AND NOT (executor_binding_id=OLD.executor_binding_id))
BEGIN
  SELECT RAISE(ABORT,'executor_binding_termination is immutable');
END;

CREATE TRIGGER executor_residual_cleanup_operation_no_replace
BEFORE INSERT ON executor_residual_cleanup_operation
WHEN EXISTS (SELECT 1 FROM executor_residual_cleanup_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'executor_residual_cleanup_operation is immutable');
END;

CREATE TRIGGER executor_residual_cleanup_operation_no_replace_update
BEFORE UPDATE ON executor_residual_cleanup_operation
WHEN EXISTS (SELECT 1 FROM executor_residual_cleanup_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'executor_residual_cleanup_operation is immutable');
END;

CREATE TRIGGER external_operation_no_replace
BEFORE INSERT ON external_operation
WHEN EXISTS (SELECT 1 FROM external_operation WHERE adapter_ref=NEW.adapter_ref AND operation_key=NEW.operation_key)
  OR EXISTS (SELECT 1 FROM external_operation WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'external_operation is immutable');
END;

CREATE TRIGGER external_operation_no_replace_update
BEFORE UPDATE ON external_operation
WHEN EXISTS (SELECT 1 FROM external_operation WHERE adapter_ref=NEW.adapter_ref AND operation_key=NEW.operation_key AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM external_operation WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'external_operation is immutable');
END;

CREATE TRIGGER external_operation_event_no_replace
BEFORE INSERT ON external_operation_event
WHEN EXISTS (SELECT 1 FROM external_operation_event WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'external_operation_event is immutable');
END;

CREATE TRIGGER external_operation_event_no_replace_update
BEFORE UPDATE ON external_operation_event
WHEN EXISTS (SELECT 1 FROM external_operation_event WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'external_operation_event is immutable');
END;

CREATE TRIGGER fleet_no_replace
BEFORE INSERT ON fleet
WHEN EXISTS (SELECT 1 FROM fleet WHERE fleet_id=NEW.fleet_id)
  OR EXISTS (SELECT 1 FROM fleet WHERE singleton=NEW.singleton)
BEGIN
  SELECT RAISE(ABORT,'fleet is immutable');
END;

CREATE TRIGGER fleet_no_replace_update
BEFORE UPDATE ON fleet
WHEN EXISTS (SELECT 1 FROM fleet WHERE fleet_id=NEW.fleet_id AND NOT (singleton=OLD.singleton))
  OR EXISTS (SELECT 1 FROM fleet WHERE singleton=NEW.singleton AND NOT (singleton=OLD.singleton))
BEGIN
  SELECT RAISE(ABORT,'fleet is immutable');
END;

CREATE TRIGGER integration_operation_no_replace
BEFORE INSERT ON integration_operation
WHEN EXISTS (SELECT 1 FROM integration_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'integration_operation is immutable');
END;

CREATE TRIGGER integration_operation_no_replace_update
BEFORE UPDATE ON integration_operation
WHEN EXISTS (SELECT 1 FROM integration_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'integration_operation is immutable');
END;

CREATE TRIGGER integration_receipt_no_replace
BEFORE INSERT ON integration_receipt
WHEN EXISTS (SELECT 1 FROM integration_receipt WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'integration_receipt is immutable');
END;

CREATE TRIGGER integration_receipt_no_replace_update
BEFORE UPDATE ON integration_receipt
WHEN EXISTS (SELECT 1 FROM integration_receipt WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'integration_receipt is immutable');
END;

CREATE TRIGGER interrupt_operation_no_replace
BEFORE INSERT ON interrupt_operation
WHEN EXISTS (SELECT 1 FROM interrupt_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'interrupt_operation is immutable');
END;

CREATE TRIGGER interrupt_operation_no_replace_update
BEFORE UPDATE ON interrupt_operation
WHEN EXISTS (SELECT 1 FROM interrupt_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'interrupt_operation is immutable');
END;

CREATE TRIGGER launch_argument_no_replace
BEFORE INSERT ON launch_argument
WHEN EXISTS (SELECT 1 FROM launch_argument WHERE operation_id=NEW.operation_id AND ordinal=NEW.ordinal)
BEGIN
  SELECT RAISE(ABORT,'launch_argument is immutable');
END;

CREATE TRIGGER launch_argument_no_replace_update
BEFORE UPDATE ON launch_argument
WHEN EXISTS (SELECT 1 FROM launch_argument WHERE operation_id=NEW.operation_id AND ordinal=NEW.ordinal AND NOT (operation_id=OLD.operation_id AND ordinal=OLD.ordinal))
BEGIN
  SELECT RAISE(ABORT,'launch_argument is immutable');
END;

CREATE TRIGGER launch_environment_no_replace
BEFORE INSERT ON launch_environment
WHEN EXISTS (SELECT 1 FROM launch_environment WHERE operation_id=NEW.operation_id AND name=NEW.name)
BEGIN
  SELECT RAISE(ABORT,'launch_environment is immutable');
END;

CREATE TRIGGER launch_environment_no_replace_update
BEFORE UPDATE ON launch_environment
WHEN EXISTS (SELECT 1 FROM launch_environment WHERE operation_id=NEW.operation_id AND name=NEW.name AND NOT (operation_id=OLD.operation_id AND name=OLD.name))
BEGIN
  SELECT RAISE(ABORT,'launch_environment is immutable');
END;

CREATE TRIGGER launch_operation_no_replace
BEFORE INSERT ON launch_operation
WHEN EXISTS (SELECT 1 FROM launch_operation WHERE binding_id=NEW.binding_id)
  OR EXISTS (SELECT 1 FROM launch_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'launch_operation is immutable');
END;

CREATE TRIGGER launch_operation_no_replace_update
BEFORE UPDATE ON launch_operation
WHEN EXISTS (SELECT 1 FROM launch_operation WHERE binding_id=NEW.binding_id AND NOT (operation_id=OLD.operation_id))
  OR EXISTS (SELECT 1 FROM launch_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'launch_operation is immutable');
END;

CREATE TRIGGER legacy_import_no_replace
BEFORE INSERT ON legacy_import
WHEN EXISTS (SELECT 1 FROM legacy_import WHERE fleet_id=NEW.fleet_id AND source_db_sha256=NEW.source_db_sha256)
  OR EXISTS (SELECT 1 FROM legacy_import WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'legacy_import is immutable');
END;

CREATE TRIGGER legacy_import_no_replace_update
BEFORE UPDATE ON legacy_import
WHEN EXISTS (SELECT 1 FROM legacy_import WHERE fleet_id=NEW.fleet_id AND source_db_sha256=NEW.source_db_sha256 AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM legacy_import WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'legacy_import is immutable');
END;

CREATE TRIGGER legacy_import_project_no_replace
BEFORE INSERT ON legacy_import_project
WHEN EXISTS (SELECT 1 FROM legacy_import_project WHERE import_id=NEW.import_id AND project_id=NEW.project_id)
BEGIN
  SELECT RAISE(ABORT,'legacy_import_project is immutable');
END;

CREATE TRIGGER legacy_import_project_no_replace_update
BEFORE UPDATE ON legacy_import_project
WHEN EXISTS (SELECT 1 FROM legacy_import_project WHERE import_id=NEW.import_id AND project_id=NEW.project_id AND NOT (import_id=OLD.import_id AND project_id=OLD.project_id))
BEGIN
  SELECT RAISE(ABORT,'legacy_import_project is immutable');
END;

CREATE TRIGGER operation_scope_claim_no_replace
BEFORE INSERT ON operation_scope_claim
WHEN EXISTS (SELECT 1 FROM operation_scope_claim WHERE operation_id=NEW.operation_id AND scope_kind=NEW.scope_kind AND scope_key=NEW.scope_key)
BEGIN
  SELECT RAISE(ABORT,'operation_scope_claim is immutable');
END;

CREATE TRIGGER operation_scope_claim_no_replace_update
BEFORE UPDATE ON operation_scope_claim
WHEN EXISTS (SELECT 1 FROM operation_scope_claim WHERE operation_id=NEW.operation_id AND scope_kind=NEW.scope_kind AND scope_key=NEW.scope_key AND NOT (operation_id=OLD.operation_id AND scope_kind=OLD.scope_kind AND scope_key=OLD.scope_key))
BEGIN
  SELECT RAISE(ABORT,'operation_scope_claim is immutable');
END;

CREATE TRIGGER plan_no_replace
BEFORE INSERT ON plan
WHEN (NEW.lifecycle='active' AND EXISTS (SELECT 1 FROM plan WHERE task_id=NEW.task_id AND lifecycle='active'))
  OR EXISTS (SELECT 1 FROM plan WHERE predecessor_plan_id=NEW.predecessor_plan_id)
  OR EXISTS (SELECT 1 FROM plan WHERE task_id=NEW.task_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM plan WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'plan is immutable');
END;

CREATE TRIGGER plan_no_replace_update
BEFORE UPDATE ON plan
WHEN (NEW.lifecycle='active' AND EXISTS (SELECT 1 FROM plan WHERE task_id=NEW.task_id AND NOT (id=OLD.id) AND lifecycle='active'))
  OR EXISTS (SELECT 1 FROM plan WHERE predecessor_plan_id=NEW.predecessor_plan_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM plan WHERE task_id=NEW.task_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM plan WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'plan is immutable');
END;

CREATE TRIGGER policy_revision_no_replace
BEFORE INSERT ON policy_revision
WHEN (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM policy_revision WHERE project_id=NEW.project_id AND superseded_at=''))
  OR EXISTS (SELECT 1 FROM policy_revision WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM policy_revision WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'policy_revision is immutable');
END;

CREATE TRIGGER policy_revision_no_replace_update
BEFORE UPDATE ON policy_revision
WHEN (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM policy_revision WHERE project_id=NEW.project_id AND NOT (id=OLD.id) AND superseded_at=''))
  OR EXISTS (SELECT 1 FROM policy_revision WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM policy_revision WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'policy_revision is immutable');
END;

CREATE TRIGGER production_operation_no_replace
BEFORE INSERT ON production_operation
WHEN EXISTS (SELECT 1 FROM production_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'production_operation is immutable');
END;

CREATE TRIGGER production_operation_no_replace_update
BEFORE UPDATE ON production_operation
WHEN EXISTS (SELECT 1 FROM production_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'production_operation is immutable');
END;

CREATE TRIGGER project_no_replace
BEFORE INSERT ON project
WHEN EXISTS (SELECT 1 FROM project WHERE fleet_id=NEW.fleet_id AND display_name=NEW.display_name)
  OR EXISTS (SELECT 1 FROM project WHERE fleet_id=NEW.fleet_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM project WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'project is immutable');
END;

CREATE TRIGGER project_no_replace_update
BEFORE UPDATE ON project
WHEN EXISTS (SELECT 1 FROM project WHERE fleet_id=NEW.fleet_id AND display_name=NEW.display_name AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM project WHERE fleet_id=NEW.fleet_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM project WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'project is immutable');
END;

CREATE TRIGGER publication_operation_no_replace
BEFORE INSERT ON publication_operation
WHEN EXISTS (SELECT 1 FROM publication_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'publication_operation is immutable');
END;

CREATE TRIGGER publication_operation_no_replace_update
BEFORE UPDATE ON publication_operation
WHEN EXISTS (SELECT 1 FROM publication_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'publication_operation is immutable');
END;

CREATE TRIGGER publication_receipt_no_replace
BEFORE INSERT ON publication_receipt
WHEN EXISTS (SELECT 1 FROM publication_receipt WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'publication_receipt is immutable');
END;

CREATE TRIGGER publication_receipt_no_replace_update
BEFORE UPDATE ON publication_receipt
WHEN EXISTS (SELECT 1 FROM publication_receipt WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'publication_receipt is immutable');
END;

CREATE TRIGGER qualification_operation_no_replace
BEFORE INSERT ON qualification_operation
WHEN EXISTS (SELECT 1 FROM qualification_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'qualification_operation is immutable');
END;

CREATE TRIGGER qualification_operation_no_replace_update
BEFORE UPDATE ON qualification_operation
WHEN EXISTS (SELECT 1 FROM qualification_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'qualification_operation is immutable');
END;

CREATE TRIGGER qualification_verdict_no_replace
BEFORE INSERT ON qualification_verdict
WHEN EXISTS (SELECT 1 FROM qualification_verdict WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'qualification_verdict is immutable');
END;

CREATE TRIGGER qualification_verdict_no_replace_update
BEFORE UPDATE ON qualification_verdict
WHEN EXISTS (SELECT 1 FROM qualification_verdict WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'qualification_verdict is immutable');
END;

CREATE TRIGGER repair_no_replace
BEFORE INSERT ON repair
WHEN EXISTS (SELECT 1 FROM repair WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'repair is immutable');
END;

CREATE TRIGGER repair_no_replace_update
BEFORE UPDATE ON repair
WHEN EXISTS (SELECT 1 FROM repair WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'repair is immutable');
END;

CREATE TRIGGER repair_resolution_no_replace
BEFORE INSERT ON repair_resolution
WHEN EXISTS (SELECT 1 FROM repair_resolution WHERE repair_id=NEW.repair_id)
BEGIN
  SELECT RAISE(ABORT,'repair_resolution is immutable');
END;

CREATE TRIGGER repair_resolution_no_replace_update
BEFORE UPDATE ON repair_resolution
WHEN EXISTS (SELECT 1 FROM repair_resolution WHERE repair_id=NEW.repair_id AND NOT (repair_id=OLD.repair_id))
BEGIN
  SELECT RAISE(ABORT,'repair_resolution is immutable');
END;

CREATE TRIGGER repair_target_no_replace
BEFORE INSERT ON repair_target
WHEN EXISTS (SELECT 1 FROM repair_target WHERE repair_id=NEW.repair_id)
BEGIN
  SELECT RAISE(ABORT,'repair_target is immutable');
END;

CREATE TRIGGER repair_target_no_replace_update
BEFORE UPDATE ON repair_target
WHEN EXISTS (SELECT 1 FROM repair_target WHERE repair_id=NEW.repair_id AND NOT (repair_id=OLD.repair_id))
BEGIN
  SELECT RAISE(ABORT,'repair_target is immutable');
END;

CREATE TRIGGER session_acquire_operation_no_replace
BEFORE INSERT ON session_acquire_operation
WHEN EXISTS (SELECT 1 FROM session_acquire_operation WHERE binding_id=NEW.binding_id)
  OR EXISTS (SELECT 1 FROM session_acquire_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'session_acquire_operation is immutable');
END;

CREATE TRIGGER session_acquire_operation_no_replace_update
BEFORE UPDATE ON session_acquire_operation
WHEN EXISTS (SELECT 1 FROM session_acquire_operation WHERE binding_id=NEW.binding_id AND NOT (operation_id=OLD.operation_id))
  OR EXISTS (SELECT 1 FROM session_acquire_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'session_acquire_operation is immutable');
END;

CREATE TRIGGER session_binding_no_replace
BEFORE INSERT ON session_binding
WHEN EXISTS (SELECT 1 FROM session_binding WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM session_binding WHERE acquire_operation_id=NEW.acquire_operation_id)
  OR EXISTS (SELECT 1 FROM session_binding WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'session_binding is immutable');
END;

CREATE TRIGGER session_binding_no_replace_update
BEFORE UPDATE ON session_binding
WHEN EXISTS (SELECT 1 FROM session_binding WHERE attempt_id=NEW.attempt_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM session_binding WHERE acquire_operation_id=NEW.acquire_operation_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM session_binding WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'session_binding is immutable');
END;

CREATE TRIGGER session_binding_release_no_replace
BEFORE INSERT ON session_binding_release
WHEN EXISTS (SELECT 1 FROM session_binding_release WHERE release_operation_id=NEW.release_operation_id)
  OR EXISTS (SELECT 1 FROM session_binding_release WHERE session_binding_id=NEW.session_binding_id)
BEGIN
  SELECT RAISE(ABORT,'session_binding_release is immutable');
END;

CREATE TRIGGER session_binding_release_no_replace_update
BEFORE UPDATE ON session_binding_release
WHEN EXISTS (SELECT 1 FROM session_binding_release WHERE release_operation_id=NEW.release_operation_id AND NOT (session_binding_id=OLD.session_binding_id))
  OR EXISTS (SELECT 1 FROM session_binding_release WHERE session_binding_id=NEW.session_binding_id AND NOT (session_binding_id=OLD.session_binding_id))
BEGIN
  SELECT RAISE(ABORT,'session_binding_release is immutable');
END;

CREATE TRIGGER session_release_operation_no_replace
BEFORE INSERT ON session_release_operation
WHEN EXISTS (SELECT 1 FROM session_release_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'session_release_operation is immutable');
END;

CREATE TRIGGER session_release_operation_no_replace_update
BEFORE UPDATE ON session_release_operation
WHEN EXISTS (SELECT 1 FROM session_release_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'session_release_operation is immutable');
END;

CREATE TRIGGER task_no_replace
BEFORE INSERT ON task
WHEN EXISTS (SELECT 1 FROM task WHERE supersedes_task_id=NEW.supersedes_task_id)
  OR EXISTS (SELECT 1 FROM task WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM task WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'task is immutable');
END;

CREATE TRIGGER task_no_replace_update
BEFORE UPDATE ON task
WHEN EXISTS (SELECT 1 FROM task WHERE supersedes_task_id=NEW.supersedes_task_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM task WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM task WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'task is immutable');
END;

CREATE TRIGGER task_archive_no_replace
BEFORE INSERT ON task_archive
WHEN EXISTS (SELECT 1 FROM task_archive WHERE task_id=NEW.task_id)
BEGIN
  SELECT RAISE(ABORT,'task_archive is immutable');
END;

CREATE TRIGGER task_archive_no_replace_update
BEFORE UPDATE ON task_archive
WHEN EXISTS (SELECT 1 FROM task_archive WHERE task_id=NEW.task_id AND NOT (task_id=OLD.task_id))
BEGIN
  SELECT RAISE(ABORT,'task_archive is immutable');
END;

CREATE TRIGGER task_hold_no_replace
BEFORE INSERT ON task_hold
WHEN EXISTS (SELECT 1 FROM task_hold WHERE task_id=NEW.task_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM task_hold WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'task_hold is immutable');
END;

CREATE TRIGGER task_hold_no_replace_update
BEFORE UPDATE ON task_hold
WHEN EXISTS (SELECT 1 FROM task_hold WHERE task_id=NEW.task_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM task_hold WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'task_hold is immutable');
END;

CREATE TRIGGER task_hold_blocked_on_task_no_replace
BEFORE INSERT ON task_hold_blocked_on_task
WHEN EXISTS (SELECT 1 FROM task_hold_blocked_on_task WHERE hold_id=NEW.hold_id)
BEGIN
  SELECT RAISE(ABORT,'task_hold_blocked_on_task is immutable');
END;

CREATE TRIGGER task_hold_blocked_on_task_no_replace_update
BEFORE UPDATE ON task_hold_blocked_on_task
WHEN EXISTS (SELECT 1 FROM task_hold_blocked_on_task WHERE hold_id=NEW.hold_id AND NOT (hold_id=OLD.hold_id))
BEGIN
  SELECT RAISE(ABORT,'task_hold_blocked_on_task is immutable');
END;

CREATE TRIGGER task_hold_decision_no_replace
BEFORE INSERT ON task_hold_decision
WHEN EXISTS (SELECT 1 FROM task_hold_decision WHERE hold_id=NEW.hold_id)
BEGIN
  SELECT RAISE(ABORT,'task_hold_decision is immutable');
END;

CREATE TRIGGER task_hold_decision_no_replace_update
BEFORE UPDATE ON task_hold_decision
WHEN EXISTS (SELECT 1 FROM task_hold_decision WHERE hold_id=NEW.hold_id AND NOT (hold_id=OLD.hold_id))
BEGIN
  SELECT RAISE(ABORT,'task_hold_decision is immutable');
END;

CREATE TRIGGER task_hold_recheck_no_replace
BEFORE INSERT ON task_hold_recheck
WHEN EXISTS (SELECT 1 FROM task_hold_recheck WHERE hold_id=NEW.hold_id)
BEGIN
  SELECT RAISE(ABORT,'task_hold_recheck is immutable');
END;

CREATE TRIGGER task_hold_recheck_no_replace_update
BEFORE UPDATE ON task_hold_recheck
WHEN EXISTS (SELECT 1 FROM task_hold_recheck WHERE hold_id=NEW.hold_id AND NOT (hold_id=OLD.hold_id))
BEGIN
  SELECT RAISE(ABORT,'task_hold_recheck is immutable');
END;

CREATE TRIGGER task_hold_resolution_no_replace
BEFORE INSERT ON task_hold_resolution
WHEN EXISTS (SELECT 1 FROM task_hold_resolution WHERE hold_id=NEW.hold_id)
BEGIN
  SELECT RAISE(ABORT,'task_hold_resolution is immutable');
END;

CREATE TRIGGER task_hold_resolution_no_replace_update
BEFORE UPDATE ON task_hold_resolution
WHEN EXISTS (SELECT 1 FROM task_hold_resolution WHERE hold_id=NEW.hold_id AND NOT (hold_id=OLD.hold_id))
BEGIN
  SELECT RAISE(ABORT,'task_hold_resolution is immutable');
END;

CREATE TRIGGER terminal_receipt_no_replace
BEFORE INSERT ON terminal_receipt
WHEN EXISTS (SELECT 1 FROM terminal_receipt WHERE attempt_id=NEW.attempt_id)
  OR EXISTS (SELECT 1 FROM terminal_receipt WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'terminal_receipt is immutable');
END;

CREATE TRIGGER terminal_receipt_no_replace_update
BEFORE UPDATE ON terminal_receipt
WHEN EXISTS (SELECT 1 FROM terminal_receipt WHERE attempt_id=NEW.attempt_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM terminal_receipt WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'terminal_receipt is immutable');
END;

CREATE TRIGGER worker_input_no_replace
BEFORE INSERT ON worker_input
WHEN EXISTS (SELECT 1 FROM worker_input WHERE executor_binding_id=NEW.executor_binding_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM worker_input WHERE answer_origin_id=NEW.answer_origin_id)
  OR EXISTS (SELECT 1 FROM worker_input WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'worker_input is immutable');
END;

CREATE TRIGGER worker_input_no_replace_update
BEFORE UPDATE ON worker_input
WHEN EXISTS (SELECT 1 FROM worker_input WHERE executor_binding_id=NEW.executor_binding_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_input WHERE answer_origin_id=NEW.answer_origin_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_input WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'worker_input is immutable');
END;

CREATE TRIGGER worker_input_acknowledgement_no_replace
BEFORE INSERT ON worker_input_acknowledgement
WHEN EXISTS (SELECT 1 FROM worker_input_acknowledgement WHERE worker_input_id=NEW.worker_input_id)
BEGIN
  SELECT RAISE(ABORT,'worker_input_acknowledgement is immutable');
END;

CREATE TRIGGER worker_input_acknowledgement_no_replace_update
BEFORE UPDATE ON worker_input_acknowledgement
WHEN EXISTS (SELECT 1 FROM worker_input_acknowledgement WHERE worker_input_id=NEW.worker_input_id AND NOT (worker_input_id=OLD.worker_input_id))
BEGIN
  SELECT RAISE(ABORT,'worker_input_acknowledgement is immutable');
END;

CREATE TRIGGER worker_input_answer_origin_no_replace
BEFORE INSERT ON worker_input_answer_origin
WHEN EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE answer_id=NEW.answer_id)
  OR EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE worker_input_id=NEW.worker_input_id)
  OR EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'worker_input_answer_origin is immutable');
END;

CREATE TRIGGER worker_input_answer_origin_no_replace_update
BEFORE UPDATE ON worker_input_answer_origin
WHEN EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE answer_id=NEW.answer_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE worker_input_id=NEW.worker_input_id AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_input_answer_origin WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'worker_input_answer_origin is immutable');
END;

CREATE TRIGGER worker_report_no_replace
BEFORE INSERT ON worker_report
WHEN EXISTS (SELECT 1 FROM worker_report WHERE attempt_id=NEW.attempt_id AND source_end_offset=NEW.source_end_offset)
  OR EXISTS (SELECT 1 FROM worker_report WHERE attempt_id=NEW.attempt_id AND source_prefix_digest=NEW.source_prefix_digest)
  OR EXISTS (SELECT 1 FROM worker_report WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'worker_report is immutable');
END;

CREATE TRIGGER worker_report_no_replace_update
BEFORE UPDATE ON worker_report
WHEN EXISTS (SELECT 1 FROM worker_report WHERE attempt_id=NEW.attempt_id AND source_end_offset=NEW.source_end_offset AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_report WHERE attempt_id=NEW.attempt_id AND source_prefix_digest=NEW.source_prefix_digest AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM worker_report WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'worker_report is immutable');
END;

CREATE TRIGGER worker_report_acknowledgement_no_replace
BEFORE INSERT ON worker_report_acknowledgement
WHEN EXISTS (SELECT 1 FROM worker_report_acknowledgement WHERE worker_report_id=NEW.worker_report_id)
BEGIN
  SELECT RAISE(ABORT,'worker_report_acknowledgement is immutable');
END;

CREATE TRIGGER worker_report_acknowledgement_no_replace_update
BEFORE UPDATE ON worker_report_acknowledgement
WHEN EXISTS (SELECT 1 FROM worker_report_acknowledgement WHERE worker_report_id=NEW.worker_report_id AND NOT (worker_report_id=OLD.worker_report_id))
BEGIN
  SELECT RAISE(ABORT,'worker_report_acknowledgement is immutable');
END;

CREATE TRIGGER worker_wake_operation_no_replace
BEFORE INSERT ON worker_wake_operation
WHEN EXISTS (SELECT 1 FROM worker_wake_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'worker_wake_operation is immutable');
END;

CREATE TRIGGER worker_wake_operation_no_replace_update
BEFORE UPDATE ON worker_wake_operation
WHEN EXISTS (SELECT 1 FROM worker_wake_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'worker_wake_operation is immutable');
END;

CREATE TRIGGER workspace_binding_no_replace
BEFORE INSERT ON workspace_binding
WHEN (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM workspace_binding WHERE physical_identity_digest=NEW.physical_identity_digest AND superseded_at=''))
  OR (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM workspace_binding WHERE project_id=NEW.project_id AND superseded_at=''))
  OR EXISTS (SELECT 1 FROM workspace_binding WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal)
  OR EXISTS (SELECT 1 FROM workspace_binding WHERE id=NEW.id)
BEGIN
  SELECT RAISE(ABORT,'workspace_binding is immutable');
END;

CREATE TRIGGER workspace_binding_no_replace_update
BEFORE UPDATE ON workspace_binding
WHEN (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM workspace_binding WHERE physical_identity_digest=NEW.physical_identity_digest AND NOT (id=OLD.id) AND superseded_at=''))
  OR (NEW.superseded_at='' AND EXISTS (SELECT 1 FROM workspace_binding WHERE project_id=NEW.project_id AND NOT (id=OLD.id) AND superseded_at=''))
  OR EXISTS (SELECT 1 FROM workspace_binding WHERE project_id=NEW.project_id AND ordinal=NEW.ordinal AND NOT (id=OLD.id))
  OR EXISTS (SELECT 1 FROM workspace_binding WHERE id=NEW.id AND NOT (id=OLD.id))
BEGIN
  SELECT RAISE(ABORT,'workspace_binding is immutable');
END;

CREATE TRIGGER worktree_binding_release_no_replace
BEFORE INSERT ON worktree_binding_release
WHEN EXISTS (SELECT 1 FROM worktree_binding_release WHERE remove_operation_id=NEW.remove_operation_id)
  OR EXISTS (SELECT 1 FROM worktree_binding_release WHERE binding_id=NEW.binding_id)
BEGIN
  SELECT RAISE(ABORT,'worktree_binding_release is immutable');
END;

CREATE TRIGGER worktree_binding_release_no_replace_update
BEFORE UPDATE ON worktree_binding_release
WHEN EXISTS (SELECT 1 FROM worktree_binding_release WHERE remove_operation_id=NEW.remove_operation_id AND NOT (binding_id=OLD.binding_id))
  OR EXISTS (SELECT 1 FROM worktree_binding_release WHERE binding_id=NEW.binding_id AND NOT (binding_id=OLD.binding_id))
BEGIN
  SELECT RAISE(ABORT,'worktree_binding_release is immutable');
END;

CREATE TRIGGER worktree_create_operation_no_replace
BEFORE INSERT ON worktree_create_operation
WHEN EXISTS (SELECT 1 FROM worktree_create_operation WHERE binding_id=NEW.binding_id)
  OR EXISTS (SELECT 1 FROM worktree_create_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'worktree_create_operation is immutable');
END;

CREATE TRIGGER worktree_create_operation_no_replace_update
BEFORE UPDATE ON worktree_create_operation
WHEN EXISTS (SELECT 1 FROM worktree_create_operation WHERE binding_id=NEW.binding_id AND NOT (operation_id=OLD.operation_id))
  OR EXISTS (SELECT 1 FROM worktree_create_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'worktree_create_operation is immutable');
END;

CREATE TRIGGER worktree_remove_operation_no_replace
BEFORE INSERT ON worktree_remove_operation
WHEN EXISTS (SELECT 1 FROM worktree_remove_operation WHERE operation_id=NEW.operation_id)
BEGIN
  SELECT RAISE(ABORT,'worktree_remove_operation is immutable');
END;

CREATE TRIGGER worktree_remove_operation_no_replace_update
BEFORE UPDATE ON worktree_remove_operation
WHEN EXISTS (SELECT 1 FROM worktree_remove_operation WHERE operation_id=NEW.operation_id AND NOT (operation_id=OLD.operation_id))
BEGIN
  SELECT RAISE(ABORT,'worktree_remove_operation is immutable');
END;
