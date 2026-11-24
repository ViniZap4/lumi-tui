package ui

import (
	"strings"
	"testing"
)

type sgrCell struct {
	r  rune
	st sgrState
}

// decodeCells replays SGR state over the printed runes of s.
func decodeCells(s string) [][]sgrCell {
	var lines [][]sgrCell
	var cur []sgrCell
	var st sgrState
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if s[j] == 'm' {
				st.apply(s[i+2 : j])
			}
			i = j + 1
			continue
		}
		if s[i] == '\n' {
			lines = append(lines, cur)
			cur = nil
			i++
			continue
		}
		r := []rune(s[i:])[0]
		cur = append(cur, sgrCell{r, st})
		i += len(string(r))
	}
	return append(lines, cur)
}

func TestCompactSGRKeepsTheLook(t *testing.T) {
	in := "\x1b[48;2;1;2;3m  \x1b[38;2;9;9;9m\x1b[0m\x1b[48;2;1;2;3m\x1b[38;2;9;9;9mtarefa\x1b[0m\x1b[48;2;1;2;3m\x1b[38;2;9;9;9m feita\x1b[0m" +
		strings.Repeat("\x1b[48;2;1;2;3m\x1b[38;2;9;9;9m \x1b[0m", 30) + "\n" +
		"\x1b[1;4;38;5;9mbold ul\x1b[22m plain-ul\x1b[24m 🙂 漢字\x1b[7m rev \x1b[m end"
	out := compactSGR(in)
	a, b := decodeCells(in), decodeCells(out)
	if len(a) != len(b) {
		t.Fatalf("line count %d != %d", len(a), len(b))
	}
	for l := range a {
		if len(a[l]) != len(b[l]) {
			t.Fatalf("line %d: %d cells != %d", l, len(a[l]), len(b[l]))
		}
		for c := range a[l] {
			x, y := a[l][c], b[l][c]
			if x.r != y.r || (x.st != y.st && !(x.r == ' ' && sameBlank(x.st, y.st))) {
				t.Fatalf("line %d cell %d: %q %+v != %q %+v", l, c, x.r, x.st, y.r, y.st)
			}
		}
	}
	if len(out)*3 > len(in) {
		t.Errorf("not compacted: %d → %d bytes", len(in), len(out))
	}
	if !strings.HasSuffix(strings.Split(out, "\n")[0], "\x1b[m") {
		t.Error("lines must end reset")
	}
}

func TestCompactSGRPassesOtherEscapes(t *testing.T) {
	in := "\x1b]8;;https://x\x07link\x1b]8;;\x07 \x1b[?25l\x1b[2Ctext"
	if out := compactSGR(in); out != in {
		t.Errorf("got %q", out)
	}
}
