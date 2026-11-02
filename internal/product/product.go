// Package product holds the single place where the product names live. The runtime
// values come from the config (instance.product_name, instance.short_name); these are
// only the defaults.
package product

const (
	// DefaultName is the full product name, shown on login pages and in tool descriptions.
	DefaultName = "Atrium Tablinum"
	// DefaultShortName is what users say in a chat ("save that in Atrium").
	DefaultShortName = "Atrium"
)
