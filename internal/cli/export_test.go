package cli

var (
	Control          = control
	WakeDigest       = wakeDigest
	TrustWait        = &trustWait
	TrustConfirm     = &trustConfirm
	TrustGrace       = &trustGrace
	ScreenWait       = &screenWait
	SubmitCheck      = &submitCheck
	SendToSupervisor = sendToSupervisor
	SubmitConfirm    = &submitConfirm
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
)
