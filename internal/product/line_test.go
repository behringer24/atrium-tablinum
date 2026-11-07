package product

import "testing"

func TestParseLine(t *testing.T) {
	l, err := ParseLine("# a comment\nline 3.4\ndate 2022-05-10   # released\n\n")
	if err != nil || l.Name != "3.4" || l.Date.Format("2006-01-02") != "2022-05-10" || l.Epoch != nil {
		t.Fatalf("%+v %v", l, err)
	}
	if got := l.Coverage().Format("2006-01-02"); got != "2022-05-10" {
		t.Errorf("coverage without epoch: %s", got)
	}
	l, err = ParseLine("line 6.2\ndate 2026-03-01\nepoch 2022-01-01\n")
	if err != nil || l.Epoch == nil || l.Coverage().Format("2006-01-02") != "2022-01-01" {
		t.Fatalf("epoch: %+v %v", l, err)
	}
	for _, bad := range []string{"", "line 3.4", "date 2022-05-10", "line 3.4\ndate soon", "line 3.4\ndate 2022-05-10\nextra 1", "line 3.4 4\ndate 2022-05-10"} {
		if _, err := ParseLine(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The line.txt in this tree must be valid: a build with a broken one would not start.
func TestTheEmbeddedLineIsValid(t *testing.T) {
	if l := CurrentLine(); l.Name == "" || l.Date.IsZero() {
		t.Fatalf("%+v", l)
	}
}
