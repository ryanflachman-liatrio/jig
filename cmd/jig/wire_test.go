package main

import "testing"

func TestNewManagerUsesPortableBuiltinRoster(t *testing.T) {
	t.Chdir(t.TempDir())
	mgr, err := newManager("")
	if err != nil {
		t.Fatal(err)
	}
	if mgr == nil {
		t.Fatal("newManager returned nil")
	}
}
