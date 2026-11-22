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
