package cli

import "context"

var (
	Control           = control
	WakeDigest        = wakeDigest
	TrustWait         = &trustWait
	TrustConfirm      = &trustConfirm
	TrustGrace        = &trustGrace
	ScreenWait        = &screenWait
	PrefillWait       = &prefillWait
	SubmitCheck       = &submitCheck
	SendToSupervisor  = sendToSupervisor
	SubmitConfirm     = &submitConfirm
	APICheckWait      = &apiCheckWait
	NoteWait          = &noteWait
	NotifyArgv        = notifyArgv
	WatchExe          = &watchExecutable
	WatcherSweep      = &watcherSweep
	ServePeer         = &servePeer
	BoardWait         = &boardStartWait
	Reconnect         = &reconnectDelay
	UpdateMin         = &updateMin
	UpdateHooks       = &updateHooks
	UnitOS            = &unitOS
	BoardHint         = boardHint
	PhoneProbe        = &phoneProbe
	RemoveIfSame      = removeIfSame
	AcceptTrust       = acceptTrust
	PollEvery         = &pollEvery
	SettleWait        = &settleWait
	SettleQuiet       = &settleQuiet
	EscGap            = &escGap
	SubmitPoll        = &submitPoll
	LaunchWait        = &launchWait
	ExitWait          = &exitWait
	KillGrace         = &killGrace
	MergeDatabaseHash = &mergeDatabaseHash
)

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
	_, _, err = r.force(ctx, st, c, caps)
	return err
}
