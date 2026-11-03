package skills

import (
	"errors"
	"strings"
	"testing"
)

const goodMD = "---\nname: sample\ndescription: A sample skill\n---\nBody\n"

func TestParseSkillMD(t *testing.T) {
	name, desc, tools, err := parseSkillMD("\ufeff---\r\nname: sample\r\ndescription: A sample skill\r\nallowed-tools: Read  Bash(git:*)\r\n---\r\nBody")
	if err != nil || name != "sample" || desc != "A sample skill" || tools != "Read Bash(git:*)" {
		t.Fatalf("%q %q %q %v", name, desc, tools, err)
	}
	_, _, tools, err = parseSkillMD("---\nname: a\ndescription: b\nallowed-tools:\n  - Read\n  - Edit\n---\n")
	if err != nil || tools != "Read Edit" {
		t.Fatalf("list form: %q %v", tools, err)
	}
	for _, bad := range []string{"", "no front matter", "---\nname: a\n", "---\nname: [unclosed\n---\n", "---\nallowed-tools: 7\nname: a\ndescription: b\n---\n"} {
		if _, _, _, err := parseSkillMD(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
}

func TestValidatePath(t *testing.T) {
	for _, ok := range []string{"SKILL.md", "references/guide.md", "data/table.csv", "a/b/c.txt", "x.JSON"} {
		if err := validatePath(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs.md", "a//b.md", "../x.md", "a/../b.md", "./x.md", "a\\b.md", ".hidden.md", "dir/.git/x.md", "dir/", "scripts/a.md",
		"Scripts/a.md", "run.sh", "tool.py", "noext", "img.png", "a\x00b.md", "a\nb.md", strings.Repeat("a", 250) + ".md"} {
		if err := validatePath(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
}

func TestPrepareDigestIsStableAndSensitive(t *testing.T) {
	a, err := prepare("sample", []File{{"SKILL.md", goodMD}, {"references/b.md", "B"}, {"references/a.md", "A"}})
	if err != nil {
		t.Fatal(err)
	}
	// The order of the files does not matter.
	b, err := prepare("sample", []File{{"references/a.md", "A"}, {"SKILL.md", goodMD}, {"references/b.md", "B"}})
	if err != nil || a.digest != b.digest {
		t.Fatalf("digest depends on the order: %v %s %s", err, a.digest, b.digest)
	}
	// Changing a file, a path or the set of files changes it.
	for name, files := range map[string][]File{
		"content": {{"SKILL.md", goodMD}, {"references/b.md", "B"}, {"references/a.md", "A!"}},
		"path":    {{"SKILL.md", goodMD}, {"references/b.md", "B"}, {"references/c.md", "A"}},
		"fewer":   {{"SKILL.md", goodMD}, {"references/b.md", "B"}},
	} {
		c, err := prepare("sample", files)
		if err != nil || c.digest == a.digest {
			t.Errorf("%s: digest did not change (%v)", name, err)
		}
	}
	if a.description != "A sample skill" || len(a.resources) != 3 || a.resources[0].Path != "SKILL.md" && a.resources[0].Path != "references/a.md" {
		t.Errorf("unexpected package: %+v", a)
	}
}

func TestPrepareRejects(t *testing.T) {
	many := []File{{"SKILL.md", goodMD}}
	for i := 0; i < MaxFiles; i++ {
		many = append(many, File{Path: "f" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".md", Content: "x"})
	}
	big := []File{{"SKILL.md", goodMD}}
	for i := 0; i < 5; i++ {
		big = append(big, File{Path: "part" + string(rune('a'+i)) + ".md", Content: strings.Repeat("a", MaxFileBytes)})
	}
	cases := map[string]struct {
		name  string
		files []File
	}{
		"too many files":    {"sample", many},
		"package too large": {"sample", big},
		"invalid utf-8":     {"sample", []File{{"SKILL.md", goodMD}, {"a.md", "\xff\xfe"}}},
		"null byte":         {"sample", []File{{"SKILL.md", goodMD}, {"a.md", "a\x00b"}}},
		"wrong name":        {"other", []File{{"SKILL.md", goodMD}}},
		"upper case name":   {"Sample", []File{{"SKILL.md", goodMD}}},
		"leading hyphen":    {"-sample", []File{{"SKILL.md", goodMD}}},
		"long description":  {"sample", []File{{"SKILL.md", "---\nname: sample\ndescription: " + strings.Repeat("d", 1025) + "\n---\n"}}},
		"case only clash":   {"sample", []File{{"SKILL.md", goodMD}, {"a.md", "1"}, {"A.md", "2"}}},
	}
	for name, c := range cases {
		if _, err := prepare(c.name, c.files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "code-review", "x1-y2-z3"} {
		if !ValidName(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "Code", "a--b", "-a", "a-", "a_b", "a b", strings.Repeat("a", 65)} {
		if ValidName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
