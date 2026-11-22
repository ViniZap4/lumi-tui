package feed

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

func parseAll(chunks ...[]byte) ([]Event, int) {
	var p Parser
	var out []Event
	for _, c := range chunks {
		out = append(out, p.Feed(c)...)
	}
	return out, p.Comments
}

func TestParserSpecFixtureEveryChunkCut(t *testing.T) {
	lf, err := os.ReadFile("testdata/spec-3.3.sse")
	if err != nil {
		t.Fatal(err)
	}
	want, comments := parseAll(lf)
	if len(want) != 7 || comments != 1 {
		t.Fatalf("got %d events / %d comments, want 7 / 1: %+v", len(want), comments, want)
	}
	names := []string{EvHello, EvVaultHead, EvVaultAccess, EvVaultUpdated, EvVaultRevoked, EvReconnect, EvSessionEnded}
	for i, n := range names {
		if want[i].Name != n {
			t.Errorf("event %d = %s, want %s", i, want[i].Name, n)
		}
	}
	variants := map[string][]byte{
		"LF":   lf,
		"CRLF": bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n")),
		"CR":   bytes.ReplaceAll(lf, []byte("\n"), []byte("\r")),
	}
	for name, data := range variants {
		for cut := 0; cut <= len(data); cut++ {
			got, _ := parseAll(data[:cut], data[cut:])
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s cut at %d: got %+v", name, cut, got)
			}
		}
		// byte-at-a-time
		var chunks [][]byte
		for i := range data {
			chunks = append(chunks, data[i:i+1])
		}
		if got, _ := parseAll(chunks...); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s byte-by-byte differs", name)
		}
	}
}

func TestParserMultilineDataDefaultNameAndBOM(t *testing.T) {
	got, _ := parseAll([]byte("\xef\xbb\xbfdata: a\ndata:b\ndata\n\nevent: x\n\n: c\nretry: 5\nid: 9\ndata: z\n\n"))
	want := []Event{{Name: "message", Data: "a\nb\n"}, {Name: "message", Data: "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodeLowercasesAndIgnoresUnknown(t *testing.T) {
	v, err := Decode(Event{Name: EvVaultHead, Data: `{"vault_id":"ABC","seq":7}`})
	if err != nil || v.(*VaultHead).VaultID != "abc" || v.(*VaultHead).Seq != 7 {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err := Decode(Event{Name: "vault.future", Data: "{"}); v != nil || err != nil {
		t.Fatalf("unknown event must be ignored: %v %v", v, err)
	}
	if _, err := Decode(Event{Name: EvHello, Data: "not json"}); err == nil {
		t.Fatal("bad JSON must error")
	}
}

// Shared fixtures (copied verbatim from apple-client Tests/Fixtures/feed).
func TestParserSharedFixtures(t *testing.T) {
	cases := []struct {
		file     string
		names    []string
		comments int
	}{
		{"stream.basic.sse", []string{EvHello, EvVaultHead, EvVaultAccess, EvVaultUpdated, EvVaultRevoked, EvReconnect}, 2},
		{"stream.crlf-multiline.sse", []string{EvHello, "vault.future_thing", EvVaultHead}, 1},
		{"stream.session-ended.sse", []string{EvHello, EvSessionEnded}, 0},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + c.file)
			if err != nil {
				t.Fatal(err)
			}
			want, comments := parseAll(data)
			if len(want) != len(c.names) || comments != c.comments {
				t.Fatalf("events %+v comments %d", want, comments)
			}
			for i, n := range c.names {
				if want[i].Name != n {
					t.Errorf("event %d = %q want %q", i, want[i].Name, n)
				}
				if _, err := Decode(want[i]); err != nil {
					t.Errorf("decode %s: %v", n, err)
				}
			}
			for cut := 0; cut <= len(data); cut++ {
				if got, _ := parseAll(data[:cut], data[cut:]); !reflect.DeepEqual(got, want) {
					t.Fatalf("cut at %d differs", cut)
				}
			}
		})
	}
	// Multi-line data joins with LF and still decodes.
	data, _ := os.ReadFile("testdata/stream.crlf-multiline.sse")
	evs, _ := parseAll(data)
	h, err := Decode(evs[0])
	if err != nil || h.(*Hello).UserID != "5b1e0c2a-9d8f-4e7a-b6c5-d4e3f2a1b0c9" {
		t.Fatalf("multiline hello: %+v %v", h, err)
	}
	if v, _ := Decode(evs[2]); v.(*VaultHead).Seq != 44 {
		t.Fatalf("field without space after colon: %+v", v)
	}
}
