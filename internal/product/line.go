package product

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// line.txt names the release line this source belongs to and the date the line was released, and
// optionally an epoch. It is embedded, so a build of 3.4.2 made years after 3.4.0 from the branch of
// that line still knows the date of 3.4.0:
//
//	line 3.4
//	date 2022-05-10
//	epoch 2022-01-01     # optional
//
//go:embed line.txt
var lineFile string

// BuildDate is the date of the commit the binary was built from, set by the release build with
//
//	-ldflags "-X github.com/behringer24/atrium-tablinum/internal/product.BuildDate=2026-11-05"
//
// It is for support and diagnosis only. Empty for a plain `go build`.
var BuildDate = ""

const dateLayout = "2006-01-02"

// Line is the release line of a build.
type Line struct {
	Name  string     // "3.4"
	Date  time.Time  // the day the line was released
	Epoch *time.Time // if set, replaces Date for licence coverage
}

// Coverage is the date a licence's maintenance is compared with: the epoch if there is one, else
// the date of the line.
func (l Line) Coverage() time.Time {
	if l.Epoch != nil {
		return *l.Epoch
	}
	return l.Date
}

// Major is the major version of the line: the number before the first dot of its name ("3" for
// "3.4"). A licence can be bound to a major version instead of a date.
func (l Line) Major() (int, error) {
	head, _, _ := strings.Cut(l.Name, ".")
	n, err := strconv.Atoi(head)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("line %q has no major version", l.Name)
	}
	return n, nil
}

// ParseLine reads the content of line.txt.
func ParseLine(text string) (Line, error) {
	var l Line
	for _, raw := range strings.Split(text, "\n") {
		s := raw
		if i := strings.Index(s, "#"); i >= 0 {
			s = s[:i]
		}
		f := strings.Fields(s)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return Line{}, fmt.Errorf("line.txt: cannot read %q", strings.TrimSpace(raw))
		}
		switch f[0] {
		case "line":
			l.Name = f[1]
		case "date", "epoch":
			d, err := time.Parse(dateLayout, f[1])
			if err != nil {
				return Line{}, fmt.Errorf("line.txt: %s: %w", f[0], err)
			}
			if f[0] == "date" {
				l.Date = d
			} else {
				l.Epoch = &d
			}
		default:
			return Line{}, fmt.Errorf("line.txt: unknown key %q", f[0])
		}
	}
	if l.Name == "" || l.Date.IsZero() {
		return Line{}, fmt.Errorf("line.txt: needs a line and a date")
	}
	return l, nil
}

// CurrentLine returns the line this binary was built from. A broken line.txt is a build error, so
// it panics at start rather than let a build with an unknown date pass for a licensed one.
func CurrentLine() Line {
	l, err := ParseLine(lineFile)
	if err != nil {
		panic(err)
	}
	return l
}
