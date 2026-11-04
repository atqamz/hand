package cli

var (
	Control          = control
	WakeDigest       = wakeDigest
	TrustWait        = &trustWait
	TrustConfirm     = &trustConfirm
	TrustGrace       = &trustGrace
	ScreenWait       = &screenWait
	PrefillWait      = &prefillWait
	SubmitCheck      = &submitCheck
	SendToSupervisor = sendToSupervisor
	SubmitConfirm    = &submitConfirm
	APICheckWait     = &apiCheckWait
	NoteWait         = &noteWait
	ServerMatch      = serverMatch
	NotifyArgv       = notifyArgv
	WatchExe         = &watchExecutable
	WatcherSweep     = &watcherSweep
	BoardWait        = &boardStartWait
	Reconnect        = &reconnectDelay
	UpdateMin        = &updateMin
	UpdateHooks      = &updateHooks
	UnitOS           = &unitOS
	BoardHint        = boardHint
	PhoneProbe       = &phoneProbe
	RemoveIfSame     = removeIfSame
	AcceptTrust      = acceptTrust
	PollEvery        = &pollEvery
	SettleWait       = &settleWait
	SettleQuiet      = &settleQuiet
	EscGap           = &escGap
	SubmitPoll       = &submitPoll
	LaunchWait       = &launchWait
	ExitWait         = &exitWait
	KillGrace        = &killGrace
)
