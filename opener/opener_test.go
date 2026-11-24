package opener

import (
	"errors"
	"reflect"
	"testing"
)

func TestAppURL(t *testing.T) {
	u, err := AppURL(Target{ServerURL: "https://lumi-api.example.com", Account: "ana maria", VaultID: "v-1", NoteID: "nota & ação"})
	if err != nil || u != "lumi://open?server=https%3A%2F%2Flumi-api.example.com&account=ana+maria&vault=v-1&note=nota+%26+a%C3%A7%C3%A3o" {
		t.Fatalf("%q %v", u, err)
	}
	u, err = AppURL(Target{LocalPath: "/Users/x/Notas", NoteRel: "dir/a b.md"})
	if err != nil || u != "lumi://open?path=%2FUsers%2Fx%2FNotas&note=dir%2Fa+b.md" {
		t.Fatalf("%q %v", u, err)
	}
	for _, bad := range []Target{
		{LocalPath: "/x", NoteRel: "../../etc/passwd"},
		{LocalPath: "/x", NoteRel: "/etc/passwd"},
		{LocalPath: "relative"},
		{ServerURL: "javascript:alert(1)", Account: "a", VaultID: "v"},
		{ServerURL: "https://x", Account: "a"},
		{ServerURL: "https://x", Account: "a\nb", VaultID: "v"},
	} {
		if _, err := AppURL(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestWebOriginAndURL(t *testing.T) {
	o, err := WebOrigin("https://lumi-api.example.com", nil)
	if err != nil || o != "https://lumi.example.com" {
		t.Fatalf("%q %v", o, err)
	}
	if _, err := WebOrigin("https://notes.example.com", nil); !errors.Is(err, ErrNoWebOrigin) {
		t.Fatalf("err %v", err)
	}
	o, err = WebOrigin("https://notes.example.com/", map[string]string{"https://notes.example.com": "https://web.example.com/"})
	if err != nil || o != "https://web.example.com" {
		t.Fatalf("override %q %v", o, err)
	}
	u, err := WebURL("https://lumi.example.com", "v 1", "n/2")
	if err != nil || u != "https://lumi.example.com/#/b/v%201/n/n%2F2" {
		t.Fatalf("%q %v", u, err)
	}
	if _, err := WebURL("file:///etc", "v", ""); err == nil {
		t.Fatal("file origin accepted")
	}
}

type call struct {
	name string
	args []string
}

func TestOpenPerPlatform(t *testing.T) {
	var calls []call
	fail := map[string]bool{}
	run := func(name string, args ...string) error {
		calls = append(calls, call{name, args})
		if fail[name] {
			return errors.New("exit 1")
		}
		return nil
	}
	app, web := "lumi://open?vault=v", "https://lumi.x/#/b/v"
	cases := []struct {
		goos     string
		forceWeb bool
		fail     map[string]bool
		used     string
		calls    []call
	}{
		{"darwin", false, nil, app, []call{{"open", []string{app}}}},
		{"darwin", true, nil, web, []call{{"open", []string{web}}}},
		{"darwin", false, map[string]bool{"open": true}, web, []call{{"open", []string{app}}, {"open", []string{web}}}},
		{"linux", false, nil, web, []call{{"xdg-open", []string{web}}}},
		{"windows", false, nil, web, []call{{"rundll32", []string{"url.dll,FileProtocolHandler", web}}}},
		{"windows", false, map[string]bool{"rundll32": true}, web, []call{{"rundll32", []string{"url.dll,FileProtocolHandler", web}}, {"cmd", []string{"/c", "start", "", web}}}},
	}
	for _, c := range cases {
		calls, fail = nil, c.fail
		used, _ := Open(c.goos, run, app, web, c.forceWeb)
		if used != c.used || !reflect.DeepEqual(calls, c.calls) {
			t.Errorf("%s force=%v: used %q calls %+v", c.goos, c.forceWeb, used, calls)
		}
	}
	calls = nil
	if _, err := Open("linux", run, app, "", false); !errors.Is(err, ErrNoWebOrigin) || len(calls) != 0 {
		t.Fatalf("no web url: %v %v", err, calls)
	}
	// cmd metacharacters are escaped in the fallback
	calls, fail = nil, map[string]bool{"rundll32": true}
	Open("windows", run, "", "https://x/?a=1&b=2", false)
	if calls[1].args[3] != "https://x/?a=1^&b=2" {
		t.Fatalf("escape %+v", calls)
	}
}
