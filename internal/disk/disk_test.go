package disk

import (
	"os"
	"testing"
)

func TestBlocks512RealFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "blocks")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := Blocks512(fi)
	if got <= 0 {
		t.Fatalf("Blocks512 = %d, want > 0", got)
	}
	if got < fi.Size() {
		t.Fatalf("Blocks512 = %d, want >= apparent size %d", got, fi.Size())
	}
}

func TestBlocks512Dir(t *testing.T) {
	fi, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := Blocks512(fi); got < 0 {
		t.Fatalf("Blocks512 = %d, want >= 0", got)
	}
}
