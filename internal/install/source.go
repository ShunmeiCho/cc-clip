// Package install holds canonical, dependency-free types shared across the
// cc-clip installation surface. Keeping these in a leaf package avoids import
// cycles between the deploy-state layer and higher-level install orchestration.
package install

// AdapterSource identifies where an installed adapter was sourced from.
type AdapterSource string

const (
	// SourceMarketplace indicates the adapter came from a marketplace.
	SourceMarketplace AdapterSource = "marketplace"
	// SourceBundled indicates the adapter shipped bundled with cc-clip.
	SourceBundled AdapterSource = "bundled"
	// SourceConfig indicates the adapter was configured/installed directly
	// (also used as the migration source for legacy boolean state).
	SourceConfig AdapterSource = "config"
)
