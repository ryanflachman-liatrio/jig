package main

import (
	"reflect"
	"testing"
)

func TestReorderArgsAllowsFlagsAroundPositionals(t *testing.T) {
	takes := map[string]bool{"root": true, "tail": true}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "flags after", args: []string{"run-id", "--tail", "5"}, want: []string{"--tail", "5", "run-id"}},
		{name: "flags before", args: []string{"--root=.state", "run-id"}, want: []string{"--root=.state", "run-id"}},
		{name: "separator", args: []string{"--root", ".state", "--", "run-id"}, want: []string{"--root", ".state", "run-id"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := reorderArgs(test.args, takes, 1)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("reorderArgs = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	if _, err := reorderArgs([]string{"one", "two"}, takes, 1); err == nil {
		t.Fatal("extra positional argument succeeded")
	}
}
