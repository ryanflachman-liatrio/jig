package config

import (
	"reflect"
	"testing"
)

func TestDefaultIsZeroValue(t *testing.T) {
	// Every table's built-in default is documented and set explicitly as
	// each unit adds fields (see internal/config/config.go). Units 2-4 will
	// extend this test as [tui]/[ui]/[notifications]/[telemetry] gain
	// non-zero defaults. NotificationsConfig's Destinations slice makes
	// Config non-comparable with ==, hence reflect.DeepEqual.
	if got := Default(); !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("Default() = %+v, want zero value (no unit has set a non-zero default yet)", got)
	}
}
