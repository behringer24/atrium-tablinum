// Package product holds the single place where the product names live. The runtime
// values come from the config (instance.product_name, instance.short_name); these are
// only the defaults.
package product

import "runtime/debug"

const (
	// DefaultName is the full product name, shown on login pages and in tool descriptions.
	DefaultName = "Atrium Tablinum"
	// DefaultShortName is what users say in a chat ("save that in Atrium").
	DefaultShortName = "Atrium"
)

// Version is the release the binary was built from. The release build sets it with
// -ldflags "-X github.com/behringer24/atrium-tablinum/internal/product.Version=1.2.3";
// a plain `go build` reports "dev".
var Version = "dev"

// Commit returns the short git revision the binary was built from, if the Go toolchain
// recorded it. Empty otherwise (for example in a build from a source archive).
func Commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return ""
}
