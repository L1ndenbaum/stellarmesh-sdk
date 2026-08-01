package envconfig

import (
	"testing"
	"time"
)

func TestParsers(t *testing.T) {
	t.Setenv("TEXT_VALUE", " value ")
	t.Setenv("DURATION_VALUE", "250ms")
	t.Setenv("INT_VALUE", "42")
	t.Setenv("BOOL_VALUE", "yes")
	t.Setenv("CSV_VALUE", "one, two,,three")

	if got := String("TEXT_VALUE", "fallback"); got != "value" {
		t.Fatalf("String() = %q", got)
	}
	if got := Duration("DURATION_VALUE", time.Second); got != 250*time.Millisecond {
		t.Fatalf("Duration() = %s", got)
	}
	if got := Int("INT_VALUE", 1); got != 42 {
		t.Fatalf("Int() = %d", got)
	}
	if !Bool("BOOL_VALUE", false) {
		t.Fatal("Bool() = false")
	}
	if got := CSV("CSV_VALUE", ""); len(got) != 3 || got[1] != "two" {
		t.Fatalf("CSV() = %#v", got)
	}
}

func TestCSVAllowEmpty(t *testing.T) {
	t.Setenv("CSV_EMPTY", "")
	if got := CSVAllowEmpty("CSV_EMPTY", "fallback"); len(got) != 0 {
		t.Fatalf("CSVAllowEmpty() = %#v", got)
	}
}
