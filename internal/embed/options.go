package embed

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// options reads the backend-specific settings (embedding.options). Every key a backend
// asks for counts as known; done() then rejects all other keys and reports the problems
// collected on the way, so a config with several mistakes fails once with all of them.
type options struct {
	backend string
	raw     map[string]string
	known   map[string]bool
	errs    []string
}

func newOptions(backend string, raw map[string]string) *options {
	return &options{backend: backend, raw: raw, known: map[string]bool{}}
}

func (o *options) fail(format string, args ...any) {
	o.errs = append(o.errs, fmt.Sprintf(format, args...))
}

func (o *options) str(key, def string) string {
	o.known[key] = true
	if v, ok := o.raw[key]; ok {
		return strings.TrimSpace(v)
	}
	return def
}

// integer reads a whole number of at least min.
func (o *options) integer(key string, def, min int) int {
	o.known[key] = true
	v, ok := o.raw[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < min {
		o.fail("options.%s: %q ist keine ganze Zahl >= %d", key, v, min)
		return def
	}
	return n
}

func (o *options) boolean(key string, def bool) bool {
	o.known[key] = true
	v, ok := o.raw[key]
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		o.fail("options.%s: %q ist weder true noch false", key, v)
		return def
	}
	return b
}

// done returns the collected problems, including keys no one asked for.
func (o *options) done() error {
	var unknown []string
	for k := range o.raw {
		if !o.known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		valid := make([]string, 0, len(o.known))
		for k := range o.known {
			valid = append(valid, k)
		}
		sort.Strings(valid)
		hint := "keine"
		if len(valid) > 0 {
			hint = strings.Join(valid, ", ")
		}
		o.fail("unbekannte options: %s (gültig für %s: %s)", strings.Join(unknown, ", "), o.backend, hint)
	}
	if len(o.errs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(o.errs, "; "))
}
