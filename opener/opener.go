// Package opener builds the URLs `lumi open` hands to the GUI clients
// (SPEC-PARTE2 §8) and launches them through an injected runner, so
// tests never open anything.
//
//   - macOS: `open "lumi://open?…"` (the Apple app registers lumi://),
//     falling back to the web URL when no app handles the scheme;
//   - Linux/BSD: `xdg-open <web url>`;
//   - Windows: `rundll32 url.dll,FileProtocolHandler <web url>`, then
//     `cmd /c start "" <url>` as a fallback.
package opener

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Target is what to open. A bound book sets ServerURL/Account/VaultID
// (and NoteID for a note); a local book sets LocalPath (and NoteRel).
type Target struct {
	ServerURL string
	Account   string
	VaultID   string
	NoteID    string
	LocalPath string
	NoteRel   string
}

// ErrNoWebOrigin means the web client address for a server is unknown.
var ErrNoWebOrigin = errors.New("web address unknown")

func clean(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("control characters are not allowed")
		}
	}
	return nil
}

func validHTTP(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("invalid server URL %q", raw)
	}
	return u, nil
}

// AppURL is the lumi:// deep link for the Apple app.
func AppURL(t Target) (string, error) {
	for _, s := range []string{t.ServerURL, t.Account, t.VaultID, t.NoteID, t.LocalPath, t.NoteRel} {
		if err := clean(s); err != nil {
			return "", err
		}
	}
	q := url.Values{}
	switch {
	case t.ServerURL != "":
		if _, err := validHTTP(t.ServerURL); err != nil {
			return "", err
		}
		if t.Account == "" || t.VaultID == "" {
			return "", fmt.Errorf("book is not fully bound (account/vault missing)")
		}
		q.Set("server", t.ServerURL)
		q.Set("account", t.Account)
		q.Set("vault", t.VaultID)
		if t.NoteID != "" {
			q.Set("note", t.NoteID)
		}
	case t.LocalPath != "":
		if !strings.HasPrefix(t.LocalPath, "/") && !(len(t.LocalPath) > 2 && t.LocalPath[1] == ':') {
			return "", fmt.Errorf("local book path must be absolute")
		}
		q.Set("path", t.LocalPath)
		if t.NoteRel != "" {
			if err := safeRel(t.NoteRel); err != nil {
				return "", err
			}
			q.Set("note", t.NoteRel)
		}
	default:
		return "lumi://open", nil
	}
	return "lumi://open?" + encode(q, []string{"server", "account", "vault", "path", "note"}), nil
}

// encode keeps the parameter order stable (readable, testable).
func encode(q url.Values, order []string) string {
	var parts []string
	for _, k := range order {
		if v, ok := q[k]; ok {
			parts = append(parts, k+"="+url.QueryEscape(v[0]))
		}
	}
	return strings.Join(parts, "&")
}

func safeRel(p string) error {
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("note path must be relative to the book")
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return fmt.Errorf("note path escapes the book")
	}
	return nil
}

// WebOrigin resolves the web client origin for a server: an explicit
// override (settings), else `lumi-api.<host>` → `lumi.<host>`.
func WebOrigin(serverURL string, overrides map[string]string) (string, error) {
	key := strings.TrimRight(serverURL, "/")
	if o, ok := overrides[key]; ok && o != "" {
		if _, err := validHTTP(o); err != nil {
			return "", err
		}
		return strings.TrimRight(o, "/"), nil
	}
	u, err := validHTTP(serverURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if strings.HasPrefix(host, "lumi-api.") {
		return u.Scheme + "://lumi." + strings.TrimPrefix(host, "lumi-api."), nil
	}
	return "", ErrNoWebOrigin
}

// WebURL is the web client deep link (`/#/b/<vault>/n/<note>`).
func WebURL(origin, vaultID, noteID string) (string, error) {
	if _, err := validHTTP(origin); err != nil {
		return "", err
	}
	if err := clean(vaultID + noteID); err != nil {
		return "", err
	}
	u := strings.TrimRight(origin, "/") + "/"
	if vaultID != "" {
		u += "#/b/" + url.PathEscape(vaultID)
		if noteID != "" {
			u += "/n/" + url.PathEscape(noteID)
		}
	}
	return u, nil
}

// Runner starts a program (exec.Command(...).Run in production).
type Runner func(name string, args ...string) error

// Open launches appURL (macOS, unless forceWeb) or webURL. It returns
// the URL it opened.
func Open(goos string, run Runner, appURL, webURL string, forceWeb bool) (string, error) {
	switch goos {
	case "darwin":
		if !forceWeb && appURL != "" {
			if err := run("open", appURL); err == nil {
				return appURL, nil
			} else if webURL == "" {
				return "", fmt.Errorf("no app handles lumi:// (%v) and the book has no web address", err)
			}
		}
		if webURL == "" {
			return "", ErrNoWebOrigin
		}
		return webURL, run("open", webURL)
	case "windows":
		if webURL == "" {
			return "", ErrNoWebOrigin
		}
		if err := run("rundll32", "url.dll,FileProtocolHandler", webURL); err == nil {
			return webURL, nil
		}
		// cmd treats & ^ | < > as operators: escape them.
		esc := strings.NewReplacer("^", "^^", "&", "^&", "|", "^|", "<", "^<", ">", "^>").Replace(webURL)
		return webURL, run("cmd", "/c", "start", "", esc)
	default:
		if webURL == "" {
			return "", ErrNoWebOrigin
		}
		return webURL, run("xdg-open", webURL)
	}
}
