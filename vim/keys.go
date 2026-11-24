package vim

import (
	"fmt"
	"strings"
)

// Keys are Bubbletea KeyMsg.String() values: printable runes as
// themselves ("j", "é", " "), named keys as "esc", "enter", "backspace",
// "tab", "delete", "up"…, and control chords as "ctrl+r".

var namedToKey = map[string]string{
	"cr": "enter", "enter": "enter", "return": "enter",
	"esc": "esc", "escape": "esc",
	"space": " ",
	"tab":   "tab",
	"bs":    "backspace", "backspace": "backspace",
	"del": "delete", "delete": "delete",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end",
	"pageup": "pgup", "pagedown": "pgdown",
	"lt":     "<",
	"bar":    "|",
	"bslash": "\\",
}

var keyToNamed = map[string]string{
	"enter": "<CR>", "esc": "<Esc>", " ": "<Space>", "tab": "<Tab>", "backspace": "<BS>",
	"delete": "<Del>", "up": "<Up>", "down": "<Down>", "left": "<Left>", "right": "<Right>",
	"home": "<Home>", "end": "<End>", "pgup": "<PageUp>", "pgdown": "<PageDown>", "<": "<lt>",
	"shift+tab": "<S-Tab>",
}

// ParseKeys turns vim key notation ("dw", "ciw<Esc>", "<C-r>a",
// "<leader>w") into keys. leader is the leader in notation ("\\", ",",
// "<Space>").
func ParseKeys(notation, leader string) ([]string, error) {
	var out []string
	rs := []rune(notation)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '<' {
			close := -1
			for j := i + 1; j < len(rs) && j-i <= 18; j++ {
				if rs[j] == '>' {
					close = j
					break
				}
			}
			if close > i+1 {
				body := string(rs[i+1 : close])
				if isKeyName(body) {
					lower := strings.ToLower(body)
					switch lower {
					case "leader", "localleader":
						lk, err := ParseKeys(leader, `\`)
						if err != nil {
							return nil, err
						}
						out = append(out, lk...)
					default:
						k, err := namedKey(body)
						if err != nil {
							return nil, err
						}
						out = append(out, k)
					}
					i = close
					continue
				}
			}
		}
		out = append(out, string(rs[i]))
	}
	return out, nil
}

func isKeyName(s string) bool {
	if len(s) > 16 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func namedKey(body string) (string, error) {
	lower := strings.ToLower(body)
	if k, ok := namedToKey[lower]; ok {
		return k, nil
	}
	parts := strings.Split(body, "-")
	key := parts[len(parts)-1]
	mods := parts[:len(parts)-1]
	if key == "" && strings.HasSuffix(body, "--") {
		key = "-"
		mods = parts[:len(parts)-2]
	}
	if len(mods) == 0 {
		if len(lower) >= 2 && lower[0] == 'f' {
			return lower, nil // f1..f12
		}
		return "", fmt.Errorf("unknown key <%s>", body)
	}
	ctrl, shift, alt := false, false, false
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "c":
			ctrl = true
		case "s":
			shift = true
		case "a", "m", "d":
			alt = true
		default:
			return "", fmt.Errorf("unknown key <%s>", body)
		}
	}
	k := strings.ToLower(key)
	if n, ok := namedToKey[k]; ok {
		k = n
		if k == " " {
			k = "space"
		}
	} else if len([]rune(key)) == 1 {
		if shift && !ctrl {
			k = strings.ToUpper(key)
			shift = false
		}
	} else {
		return "", fmt.Errorf("unknown key <%s>", body)
	}
	out := k
	if shift {
		out = "shift+" + out
	}
	if ctrl {
		out = "ctrl+" + out
	}
	if alt {
		out = "alt+" + out
	}
	return out, nil
}

// KeysToNotation renders keys back to vim notation (macro registers,
// the showcmd area).
func KeysToNotation(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(KeyNotation(k))
	}
	return b.String()
}

// KeyNotation is one key in vim notation.
func KeyNotation(k string) string {
	if n, ok := keyToNamed[k]; ok {
		return n
	}
	if strings.HasPrefix(k, "ctrl+") {
		return "<C-" + strings.TrimPrefix(k, "ctrl+") + ">"
	}
	if strings.HasPrefix(k, "alt+") {
		return "<M-" + strings.TrimPrefix(k, "alt+") + ">"
	}
	return k
}

// isPrintable reports a key that inserts text.
func isPrintable(k string) bool {
	if k == "" {
		return false
	}
	if k == " " {
		return true
	}
	rs := []rune(k)
	if len(rs) == 1 {
		return rs[0] >= ' ' && rs[0] != 0x7f
	}
	// Multi-rune KeyRunes (a paste or IME commit) is printable unless it
	// is a named key.
	if strings.HasPrefix(k, "ctrl+") || strings.HasPrefix(k, "alt+") || strings.HasPrefix(k, "shift+") {
		return false
	}
	switch k {
	case "enter", "esc", "tab", "backspace", "delete", "up", "down", "left", "right", "home", "end", "pgup", "pgdown",
		"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12", "insert":
		return false
	}
	return true
}
