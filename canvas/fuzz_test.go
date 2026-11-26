package canvas

import (
	"encoding/json"
	"strings"
	"testing"
)

// Fuzz targets for the JSON Canvas reader (Phase 3 quality harness,
// docs/phase3/research/09 Q6):
//
//	go test ./canvas -run '^$' -fuzz FuzzParse -fuzztime 2m

func seedCanvases(f *testing.F) {
	f.Add(EmptyJSON)
	f.Add("")
	f.Add(`{"nodes":[{"id":"a","type":"text","text":"# Olá","x":0,"y":0,"width":10,"height":10}],"edges":[]}`)
	f.Add("{\n\t\"nodes\":[\n\t\t{\"id\":\"a\",\"type\":\"text\"}\n\t\t{\"id\":\"b\",\"type\":\"text\"}\n\t],\n\t\"edges\":[]\n}")
	f.Add(`{"nodes":[{"id":"g","type":"group","x":-10,"y":-10,"width":100,"height":100},{"id":"f","type":"file","file":"a.md","subpath":"#h","x":0,"y":0,"width":1,"height":1}],"edges":[{"id":"e","fromNode":"g","toNode":"f","fromEnd":"arrow","label":"\"}{\""}]}`)
	f.Add(`[{}{}]`)
	f.Add(`{"nodes":[{"id":"\\"}{"}]}`)
}

// FuzzParse: Parse never panics, and whatever it accepts can be outlined.
func FuzzParse(f *testing.F) {
	seedCanvases(f)
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Parse([]byte(s))
		if err != nil {
			return
		}
		rows := c.Outline()
		for _, r := range rows {
			if r.Kind == RowText || r.Kind == RowFile || r.Kind == RowLink || r.Kind == RowGroup {
				_ = c.TextOf(r.NodeID)
			}
		}
	})
}

// FuzzHealMissingCommas: healing only ever inserts commas, never on valid
// JSON, and each inserted comma sits between a `}` and the next `{`.
func FuzzHealMissingCommas(f *testing.F) {
	seedCanvases(f)
	f.Fuzz(func(t *testing.T, s string) {
		healed, ok := HealMissingCommas(s)
		if !ok {
			if healed != "" {
				t.Fatalf("ok=false but returned %q", healed)
			}
			return
		}
		if json.Valid([]byte(s)) {
			t.Fatalf("healed valid JSON %q -> %q", s, healed)
		}
		if len(healed) <= len(s) {
			t.Fatalf("ok=true but nothing inserted: %q", healed)
		}
		// Removing the inserted commas restores the input byte for byte:
		// walk both strings; every extra byte in healed must be a ','
		// that directly follows a '}'.
		i, j := 0, 0
		for i < len(s) && j < len(healed) {
			if s[i] == healed[j] {
				i++
				j++
				continue
			}
			if healed[j] != ',' || j == 0 || healed[j-1] != '}' {
				t.Fatalf("unexpected change at %d: %q -> %q", j, s, healed)
			}
			j++
		}
		if i != len(s) || strings.Trim(healed[j:], ",") != "" || (j < len(healed) && healed[j-1] != '}') {
			t.Fatalf("healed is not input plus commas: %q -> %q", s, healed)
		}
	})
}
