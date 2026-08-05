package sysconfig

import (
	"testing"
	"time"
)

func TestMaskSensitive(t *testing.T) {
	if maskSensitive("") != "" {
		t.Error("empty should stay empty")
	}
	if maskSensitive("ab") != "****" {
		t.Errorf("short string should be ****, got %q", maskSensitive("ab"))
	}
	if got := maskSensitive("abcdefghijklmnop"); got != "abcd************" {
		t.Errorf("got %q", got)
	}
}

func TestAtoiSafe(t *testing.T) {
	if atoiSafe("42", 0) != 42 {
		t.Error("42 should parse")
	}
	if atoiSafe("xx", 7) != 7 {
		t.Error("non-numeric should fall back")
	}
	if atoiSafe("", 9) != 9 {
		t.Error("empty should fall back")
	}
}

func TestParseDurationSafe(t *testing.T) {
	if parseDurationSafe("30s") != 30*time.Second {
		t.Error("30s should parse")
	}
	if parseDurationSafe("bogus") != time.Second {
		t.Error("invalid should fall back to 1s")
	}
	if parseDurationSafe("") != time.Second {
		t.Error("empty should fall back to 1s")
	}
}
