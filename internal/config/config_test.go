package config

import (
	"reflect"
	"testing"
)

func TestDefaultIsZeroValue(t *testing.T) {
	// Non-zero defaults live in XOrDefault methods, never in Default (see
	// its doc comment). Config's slices make it non-comparable with ==,
	// hence reflect.DeepEqual.
	if got := Default(); !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("Default() = %+v, want zero value", got)
	}
}
