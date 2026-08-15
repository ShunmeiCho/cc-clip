package main

import "github.com/shunmei/cc-clip/internal/shim"

// shouldAdoptRemoteBinMode decides whether a run that did NOT pass
// --use-remote-bin should still treat the host as package-managed because a
// previous deploy recorded it as such (DeployState.UseRemoteBin, #110).
//
// Without this stickiness, the mode lasts exactly one run: `cc-clip update`
// prints `cc-clip connect <host> --force` as its redeploy reminder, and
// pasting that line would re-upload to ~/.local/bin/cc-clip, shadowing the
// package manager's binary the flag exists to preserve. An explicit
// --local-bin is the deliberate way back to uploaded deploys, so it wins over
// the recorded mode.
func shouldAdoptRemoteBinMode(useRemoteBinFlag bool, localBinFlag string, state *shim.DeployState) bool {
	return !useRemoteBinFlag && localBinFlag == "" && state != nil && state.UseRemoteBin
}

// remoteBinChanged reports whether the package-managed remote executable
// differs from what deploy state last recorded. On the --use-remote-bin path
// nothing is uploaded, so `needsUpload` stays false forever — this is the
// remote-bin counterpart that keeps the x11-bridge restart decision honest
// when the package manager upgraded the binary underneath us. A missing prior
// state counts as changed: with nothing to compare against, restarting the
// bridge is the safe direction.
func remoteBinChanged(existing *shim.RemoteBinaryInfo, prior *shim.DeployState) bool {
	return existing != nil && (prior == nil || prior.BinaryHash != existing.Hash)
}
