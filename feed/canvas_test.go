package feed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinizap/lumi/tui-client/account"
)

// Canvases (server contract): id = path = `<slug>.canvas`, raw JSON in
// the file, never frontmatter — on pull, update and push.
func TestCatchUpWritesCanvasAsRawJSON(t *testing.T) {
	root := t.TempDir()
	json1 := `{"nodes":[{"id":"a","type":"text","x":0,"y":0,"width":10,"height":10,"text":"oi"}],"edges":[]}`
	api := &fakeAPI{
		pages: map[int64]*account.ChangesPage{0: {Notes: []account.ChangedNote{
			{RemoteNote: account.RemoteNote{ID: "board.canvas", Path: "board.canvas", Title: "board", Kind: "canvas"}, ChangeSeq: 1}}, Head: 1}},
		texts: map[string]string{"board.canvas": json1},
	}
	c := newCache()
	if _, err := CatchUp(context.Background(), api, "v", root, c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "board.canvas"))
	if err != nil || strings.Contains(string(data), "---") || strings.TrimSpace(string(data)) != json1 {
		t.Fatalf("canvas file %q %v", data, err)
	}
	if _, ok := c.Notes["board.canvas"]; !ok {
		t.Fatal("canvas not recorded under its path id")
	}
	// a remote update rewrites the JSON in place, still without frontmatter
	json2 := strings.Replace(json1, `"oi"`, `"olá"`, 1)
	api.texts["board.canvas"] = json2
	api.pages[1] = &account.ChangesPage{Notes: []account.ChangedNote{
		{RemoteNote: account.RemoteNote{ID: "board.canvas", Path: "board.canvas", Kind: "canvas"}, ChangeSeq: 2}}, Head: 2}
	if _, err := CatchUp(context.Background(), api, "v", root, c); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "board.canvas"))
	if strings.Contains(string(data), "---") || strings.TrimSpace(string(data)) != json2 {
		t.Fatalf("updated canvas %q", data)
	}
}

func TestPushCanvasUsesPathIDAndNoFrontmatter(t *testing.T) {
	e := newPushEnv(t)
	json1 := `{"nodes":[],"edges":[]}`
	e.srv.AddNote("plan.canvas", "plan.canvas", json1)
	snap, err := e.cl.GetNoteSnapshot(context.Background(), "v1", "plan.canvas")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.path("plan.canvas"), []byte(json1+"\n"), 0o644)
	if err := e.s.Record("plan.canvas", e.path("plan.canvas"), snap); err != nil {
		t.Fatal(err)
	}
	edited := `{"nodes":[{"id":"n","type":"text","x":1,"y":2,"width":3,"height":4,"text":"x"}],"edges":[]}`
	os.WriteFile(e.path("plan.canvas"), []byte(edited+"\n"), 0o644)
	if p := e.s.Pending(); len(p) != 1 || p[0] != "plan.canvas" {
		t.Fatalf("pending %v", p)
	}
	res, err := e.s.Push(context.Background(), e.path("plan.canvas"))
	if err != nil || res.Outcome != PushMerged {
		t.Fatalf("push %+v %v", res, err)
	}
	if got := e.srv.Text("plan.canvas"); strings.TrimSpace(got) != edited {
		t.Fatalf("server text %q", got)
	}
	data, _ := os.ReadFile(e.path("plan.canvas"))
	if strings.Contains(string(data), "---") {
		t.Fatalf("frontmatter written into a canvas: %q", data)
	}
}
