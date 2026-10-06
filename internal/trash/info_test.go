package trash

import (
	"testing"
	"time"
)

func TestPercentRoundTrip(t *testing.T) {
	cases := []string{
		"/home/user/My File.txt",
		"/home/user/100%done",
		"/home/user/naïve/résumé",
		"/home/user/a+b&c=d",
		"/home/user/emoji-\U0001F600",
	}
	for _, c := range cases {
		enc := percentEncode(c)
		dec, err := percentDecode(enc)
		if err != nil {
			t.Fatalf("decode %q: %v", enc, err)
		}
		if dec != c {
			t.Fatalf("round trip mismatch: %q -> %q -> %q", c, enc, dec)
		}
	}
}

func TestPercentEncodeKeepsSlash(t *testing.T) {
	got := percentEncode("/a b/c")
	want := "/a%20b/c"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPercentDecodeErrors(t *testing.T) {
	if _, err := percentDecode("/a%2"); err == nil {
		t.Fatal("expected truncated escape error")
	}
	if _, err := percentDecode("/a%ZZ"); err == nil {
		t.Fatal("expected invalid escape error")
	}
}

func TestEncodeInfoGolden(t *testing.T) {
	when := time.Date(2026, 10, 5, 12, 0, 0, 0, time.Local)
	got := string(encodeInfo("/home/u/My File.txt", when))
	want := "[Trash Info]\nPath=/home/u/My%20File.txt\nDeletionDate=2026-10-05T12:00:00\n"
	if got != want {
		t.Fatalf("golden mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestParseInfoRoundTrip(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	data := encodeInfo("/home/u/résumé final.pdf", when)
	info, err := parseInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "/home/u/résumé final.pdf" {
		t.Fatalf("path mismatch: %q", info.Path)
	}
	if !info.DeletionDate.Equal(when) {
		t.Fatalf("date mismatch: %v vs %v", info.DeletionDate, when)
	}
}

func TestParseInfoRejectsBadHeader(t *testing.T) {
	if _, err := parseInfo([]byte("[Wrong]\nPath=/x\n")); err == nil {
		t.Fatal("expected header error")
	}
}
