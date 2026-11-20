package qr

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEncodeBasic(t *testing.T) {
	c, err := Encode("HELLO WORLD", Options{Level: Q, Mask: -1})
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Mode != Alphanumeric || c.Size != 21 {
		t.Fatalf("version %d mode %v size %d", c.Version, c.Mode, c.Size)
	}
	if _, err := Encode(strings.Repeat("x", 3000), Options{Level: H, Mask: -1}); err == nil {
		t.Fatal("too long text must fail")
	}
}

// TestDump writes matrices for the external cross-check (QR_DUMP=file).
func TestDump(t *testing.T) {
	out := os.Getenv("QR_DUMP")
	if out == "" {
		t.Skip("QR_DUMP not set")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	texts := []string{"12345", "HELLO WORLD", "https://leotech.cz", "VAVJS1339FH354618", "Příliš žluťoučký kůň",
		strings.Repeat("LeoDraw MCP 0123456789 ", 20), strings.Repeat("A1", 400), strings.Repeat("9", 2000)}
	for ti, text := range texts {
		for l := L; l <= H; l++ {
			for _, mask := range []int{-1, 0, 3, 5, 7} {
				c, err := Encode(text, Options{Level: l, Mask: mask})
				if err != nil {
					continue
				}
				fmt.Fprintf(f, "%d %s %d %d %s %d\n", ti, "LMQH"[l:l+1], c.Version, c.Mask, c.Mode, mask)
				for _, row := range c.Modules {
					for _, d := range row {
						if d {
							f.WriteString("1")
						} else {
							f.WriteString("0")
						}
					}
					f.WriteString("\n")
				}
			}
		}
	}
}
