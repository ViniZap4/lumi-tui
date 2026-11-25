package ui

import "testing"

// Same cases as the web client's fileops tests (yazi grammar).
func TestPlanCreate(t *testing.T) {
	cases := []struct {
		dir, in string
		want    createPlan
	}{
		{"", "Idea", createPlan{kind: planNote, title: "Idea"}},
		{"a", "b/Idea", createPlan{kind: planNote, title: "Idea", folder: "a/b"}},
		{"a/b", "/top", createPlan{kind: planNote, title: "top"}},
		{"a/b", "../x.md", createPlan{kind: planNote, title: "x", folder: "a"}},
		{"a", "dir/", createPlan{kind: planFolder, path: "a/dir"}},
		{"a", "map.canvas", createPlan{kind: planCanvas, path: "a/map.canvas"}},
		{"", "b/Plano.CANVAS", createPlan{kind: planCanvas, path: "b/Plano.CANVAS"}},
		{"", "../../../etc/passwd", createPlan{kind: planNote, title: "passwd", folder: "etc"}},
		{"", "  ", createPlan{err: "digite um nome (termine com / para pasta)"}},
		{"", "..", createPlan{err: "isso é a raiz do livro"}},
		{"", ".lumi/x", createPlan{err: "“.lumi”: nomes não podem começar com ponto"}},
		{"", `a\b`, createPlan{kind: planNote, title: "b", folder: "a"}},
		{"", "a/ b/c", createPlan{err: "“ b”: nomes não podem começar ou terminar com espaço"}},
	}
	for _, c := range cases {
		if got := planCreate(c.dir, c.in); got != c.want {
			t.Errorf("planCreate(%q, %q) = %+v, want %+v", c.dir, c.in, got, c.want)
		}
	}
}

func TestPlanRename(t *testing.T) {
	cases := []struct {
		path, title, in string
		want            renamePlan
	}{
		{"a/n.md", "N", "New", renamePlan{kind: planNote, title: "New"}},
		{"a/n.md", "N", "N", renamePlan{kind: planNone}},
		{"a/n.md", "N", "other.md", renamePlan{kind: planNote, path: "a/other.md"}},
		{"a/n.md", "N", "b/", renamePlan{kind: planNote, path: "a/b/n.md"}},
		{"a/n.md", "N", "/x/Title", renamePlan{kind: planNote, path: "x/n.md", title: "Title"}},
		{"a/n.md", "N", "../N", renamePlan{kind: planNote, path: "n.md"}},
		{"a/n.md", "N", "../.hidden/", renamePlan{err: "“.hidden”: nomes não podem começar com ponto"}},
		{"a/n.md", "N", "", renamePlan{err: "digite o novo nome"}},
	}
	for _, c := range cases {
		if got := planRename(c.path, c.title, c.in); got != c.want {
			t.Errorf("planRename(%q, %q) = %+v, want %+v", c.path, c.in, got, c.want)
		}
	}
}

func TestPlanFolderRename(t *testing.T) {
	cases := []struct {
		from, in string
		want     folderRenamePlan
	}{
		{"a/b", "c", folderRenamePlan{kind: planFolder, from: "a/b", to: "a/c"}},
		{"a/b", "/c/", folderRenamePlan{kind: planFolder, from: "a/b", to: "c"}},
		{"a/b", "b", folderRenamePlan{kind: planNone}},
		{"a/b", "b/inner", folderRenamePlan{err: "uma pasta não pode ir para dentro dela mesma"}},
		{"a", "..", folderRenamePlan{err: "isso é a raiz do livro"}},
	}
	for _, c := range cases {
		if got := planFolderRename(c.from, c.in); got != c.want {
			t.Errorf("planFolderRename(%q, %q) = %+v, want %+v", c.from, c.in, got, c.want)
		}
	}
}

func TestCopyTitle(t *testing.T) {
	if got := copyTitle("A", map[string]bool{}); got != "A copy" {
		t.Fatal(got)
	}
	if got := copyTitle("A", map[string]bool{"A copy": true, "A copy 2": true}); got != "A copy 3" {
		t.Fatal(got)
	}
}

func TestFuzzyScore(t *testing.T) {
	if fuzzyScore("xyz", "Guide") >= 0 {
		t.Fatal("non-subsequence matched")
	}
	if fuzzyScore("gd", "Guide") < 0 {
		t.Fatal("subsequence missed")
	}
	if fuzzyScore("gui", "Guide") <= fuzzyScore("gui", "a long g u i title") {
		t.Fatal("prefix run should outrank scattered match")
	}
	if fuzzyScore("", "x") != 0 {
		t.Fatal("empty query")
	}
}
