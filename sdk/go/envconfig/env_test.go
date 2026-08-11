package envconfig

import (
	"strings"
	"testing"
	"time"
)

func TestParsers(t *testing.T) {
	t.Setenv("TEXT_VALUE", " value ")
	t.Setenv("DURATION_VALUE", "250ms")
	t.Setenv("INT_VALUE", "42")
	t.Setenv("BOOL_VALUE", "yes")
	t.Setenv("CSV_VALUE", "one, two,,three")
	t.Setenv("BYTE_SIZE_VALUE", "16MiB")

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
	if got := ByteSize("BYTE_SIZE_VALUE", 1); got != 16<<20 {
		t.Fatalf("ByteSize() = %d", got)
	}
}

func TestStrictLoaderRejectsInvalidExplicitValues(t *testing.T) {
	t.Setenv("INVALID_DURATION", "forever")
	t.Setenv("INVALID_INT", "many")
	t.Setenv("INVALID_BYTES", "999999999999999999999GiB")
	t.Setenv("INVALID_BOOL", "sometimes")
	t.Setenv("INVALID_CSV", " , ")

	tests := []struct {
		name string
		load func(*StrictLoader)
		key  string
	}{
		{name: "duration", load: func(loader *StrictLoader) { loader.Duration("INVALID_DURATION", time.Second) }, key: "INVALID_DURATION"},
		{name: "int", load: func(loader *StrictLoader) { loader.Int("INVALID_INT", 1) }, key: "INVALID_INT"},
		{name: "bytes", load: func(loader *StrictLoader) { loader.ByteSize("INVALID_BYTES", 1) }, key: "INVALID_BYTES"},
		{name: "bool", load: func(loader *StrictLoader) { loader.Bool("INVALID_BOOL", true) }, key: "INVALID_BOOL"},
		{name: "csv", load: func(loader *StrictLoader) { loader.CSV("INVALID_CSV", "fallback") }, key: "INVALID_CSV"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loader := NewStrictLoader()
			test.load(loader)
			if err := loader.Err(); err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("Err() = %v", err)
			}
		})
	}
}

func TestStrictLoaderUsesFallbackForUnsetValues(t *testing.T) {
	loader := NewStrictLoader()
	if got := loader.Duration("UNSET_DURATION", time.Second); got != time.Second {
		t.Fatalf("Duration() = %s", got)
	}
	if got := loader.Int("UNSET_INT", 3); got != 3 {
		t.Fatalf("Int() = %d", got)
	}
	if got := loader.ByteSize("UNSET_BYTES", 4); got != 4 {
		t.Fatalf("ByteSize() = %d", got)
	}
	if got := loader.Bool("UNSET_BOOL", true); !got {
		t.Fatal("Bool() = false")
	}
	if got := loader.CSV("UNSET_CSV", "one,two"); len(got) != 2 {
		t.Fatalf("CSV() = %v", got)
	}
	if err := loader.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestCSVAllowEmpty(t *testing.T) {
	t.Setenv("CSV_EMPTY", "")
	if got := CSVAllowEmpty("CSV_EMPTY", "fallback"); len(got) != 0 {
		t.Fatalf("CSVAllowEmpty() = %#v", got)
	}
}
