package exitcode

const (
	Success           = 0
	NoImage           = 10
	TunnelUnreachable = 11
	TokenInvalid      = 12
	DownloadFailed    = 13
	// InstallBlocked indicates an install source chain hard-stopped without
	// installing (e.g. consent refused, policy forbidden, verification failed,
	// or all sources exhausted). Produced by the Step 5 install caller; defined
	// here so that caller can map InstallOutcome.Outcome == OutcomeHardStop to a
	// stable, scriptable exit code in the business band.
	InstallBlocked = 14
	InternalError  = 20
)
