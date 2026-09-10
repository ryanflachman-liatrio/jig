package scaffold

import (
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	t.Run("lookup hit", func(t *testing.T) {
		got, err := Lookup("minimal")
		if err != nil {
			t.Fatalf("Lookup: %v", err)
		}
		if got.Name != "minimal" || got.AssetDir != "templates/minimal" || got.Description == "" {
			t.Fatalf("Lookup(minimal) = %#v", got)
		}
	})

	t.Run("unknown lists valid names", func(t *testing.T) {
		_, err := Lookup("missing")
		if err == nil {
			t.Fatal("Lookup(missing) unexpectedly succeeded")
		}
		if !strings.Contains(err.Error(), "minimal") {
			t.Fatalf("Lookup(missing) error %q does not list valid templates", err)
		}
	})

	t.Run("all is ordered and isolated", func(t *testing.T) {
		all := All()
		if len(all) != 1 || all[0].Name != "minimal" {
			t.Fatalf("All() = %#v", all)
		}
		all[0].Name = "changed"
		if got := All()[0].Name; got != "minimal" {
			t.Fatalf("mutating All result changed registry to %q", got)
		}
	})
}
