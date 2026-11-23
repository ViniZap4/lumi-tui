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
