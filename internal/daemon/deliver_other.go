//go:build !darwin

package daemon

// platformDeliverer returns nil on non-darwin platforms.
// No platform-specific notification adapter is available.
func platformDeliverer() Deliverer {
	return nil
}
