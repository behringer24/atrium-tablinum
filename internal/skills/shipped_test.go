package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// The skills shipped in skills/ must pass the checks of store_skill, so that an organisation can
// store them as they are.
func TestShippedSkills(t *testing.T) {
	root := filepath.Join("..", "..", "skills")
	dirs, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(root, d.Name())
		var files []File
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			files = append(files, File{Path: filepath.ToSlash(rel), Content: string(b)})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepare(d.Name(), files); err != nil {
			t.Errorf("%s: %v", d.Name(), err)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no shipped skills found")
	}
}
