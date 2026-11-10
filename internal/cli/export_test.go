package cli

import "context"

var (
	WakeDigest       = wakeDigest
	TrustWait        = &trustWait
	TrustConfirm     = &trustConfirm
	TrustGrace       = &trustGrace
	QuietGrace       = &quietGrace
	ScreenWait       = &screenWait
	PrefillWait      = &prefillWait
	SubmitCheck      = &submitCheck
	SendToSupervisor = sendToSupervisor
	SubmitConfirm    = &submitConfirm
	APICheckWait     = &apiCheckWait
	NoteWait         = &noteWait
	NotifyArgv       = notifyArgv
	WatchExe         = &watchExecutable
	WatcherSweep     = &watcherSweep
	ServePeer        = &servePeer
	BoardWait        = &boardStartWait
	Reconnect        = &reconnectDelay
	UpdateMin        = &updateMin
	UpdateHooks      = &updateHooks
	UnitOS           = &unitOS
	BoardHint        = boardHint
	RemoveIfSame     = removeIfSame
	AppLink          = appLink
	AcceptTrust      = acceptTrust
	PollEvery        = &pollEvery
	SettleWait       = &settleWait
	SettleQuiet      = &settleQuiet
	EscGap           = &escGap
	SubmitPoll       = &submitPoll
	LaunchWait       = &launchWait
	ExitWait         = &exitWait
	KillGrace        = &killGrace
	ImportWrite      = &importWriteFile
)

func Control(env Env, home string) func(context.Context, ...string) (string, error) {
	return control(env, home, "")
}

func EnsureWatcher(ctx context.Context, env Env, home, root, generation string) (bool, error) {
	r := &runner{env: env, home: home, root: root, generation: generation}
	st, err := r.store()
	if err != nil {
		return false, err
	}
	st.Close()
	return r.ensureWatcher(ctx)
}

func Force(ctx context.Context, env Env, home string) error {
	r := &runner{env: env, home: home}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	c, caps, err := r.luvus(context.Background())
	if err != nil {
		return err
	}
	_, _, err = r.force(ctx, st, c, caps, "")
	return err
}

var (
	HostIdentity = &hostIdentity
	HostBeat     = &hostBeatEvery
	ErrHostLost  = errHostLost
)

func BeatHost(env Env, home string) error { return (&runner{env: env, home: home}).beatHost() }

func ReleaseHost(env Env, home string) { (&runner{env: env, home: home}).releaseHost() }
