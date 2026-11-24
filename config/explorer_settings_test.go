package config

import "testing"

func TestExplorerSettingsRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := Load()
	if !c.ShowPreview || !c.ShowLegend || !c.PaneNav || !c.Mouse || c.ShowHidden {
		t.Fatalf("defaults = %+v", c)
	}
	c.ShowPreview, c.ShowLegend, c.PaneNav, c.Mouse, c.ShowHidden = false, false, false, false, true
	c.Editor, c.EditorArgs = "hx", []string{"--vsplit"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	r := Load()
	if r.ShowPreview || r.ShowLegend || r.PaneNav || r.Mouse || !r.ShowHidden || r.Editor != "hx" || len(r.EditorArgs) != 1 {
		t.Fatalf("reloaded = %+v", r)
	}
}

func TestNoteSettingsRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := Load()
	if c.DefaultEditor != "internal" || c.OpenIn != "reader" || !c.Autosave || !c.ClipboardSync || c.ReduceMotion || c.EscapeTimeoutMs != 300 {
		t.Fatalf("defaults = %+v", c)
	}
	c.DefaultEditor, c.OpenIn = "external", "source"
	c.EscapeSequences = []string{"jj", "ff"}
	c.ReduceMotion, c.Autosave = true, false
	c.WebOrigins["https://lumi-api.example.com"] = "https://notes.example.com"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	r := Load()
	if r.DefaultEditor != "external" || r.OpenIn != "source" || len(r.EscapeSequences) != 2 || r.EscapeSequences[1] != "ff" ||
		!r.ReduceMotion || r.Autosave || r.WebOrigins["https://lumi-api.example.com"] != "https://notes.example.com" {
		t.Fatalf("reloaded = %+v", r)
	}
}
