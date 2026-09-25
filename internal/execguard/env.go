package execguard

// Protocol versions the handoff and every guard record. It is also the secret-ref
// material of the HAND_WORKER_CREDENTIAL Launch environment row.
const Protocol = "hand-exec-guard:v1"

const (
	CredentialEnv      = "HAND_WORKER_CREDENTIAL"
	ExecutorBindingEnv = "HAND_WORKER_EXECUTOR_BINDING"
)
