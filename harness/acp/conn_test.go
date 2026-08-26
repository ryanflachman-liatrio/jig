package acp

import (
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestSelectOptions(t *testing.T) {
	options := acpsdk.SessionConfigSelectOptionsUngrouped{
		{Value: "low"},
		{Value: "high"},
	}
	conn := &Conn{}
	conn.setSessionConfig("session", []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:       "adapter-effort",
			Category: categoryPtr(acpsdk.SessionConfigOptionCategoryThoughtLevel),
			Options:  acpsdk.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}})

	got, ok := conn.selectOptions("session", "adapter-effort")
	if !ok || !containsConfigValue(got, "high") {
		t.Fatalf("selectOptions() = %v, %t; want advertised high", got, ok)
	}
	if got, ok := conn.selectOptionIDByCategory("session", acpsdk.SessionConfigOptionCategoryThoughtLevel); !ok || got != "adapter-effort" {
		t.Fatalf("selectOptionIDByCategory() = %q, %t; want adapter-effort", got, ok)
	}
	if _, ok := conn.selectOptions("session", "model"); ok {
		t.Fatal("selectOptions(model) reported an option that was not advertised")
	}
}

func TestSetSelectConfigRejectsUnavailableValue(t *testing.T) {
	options := acpsdk.SessionConfigSelectOptionsUngrouped{{Value: "medium"}}
	conn := &Conn{}
	conn.setSessionConfig("session", []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:       "adapter-effort",
			Category: categoryPtr(acpsdk.SessionConfigOptionCategoryThoughtLevel),
			Options:  acpsdk.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}})

	err := conn.SetSelectConfig(t.Context(), "session", "adapter-effort", "high")
	if err == nil || !strings.Contains(err.Error(), "adapter advertises medium") {
		t.Fatalf("SetEffort() error = %v, want advertised values", err)
	}
}

func categoryPtr(category acpsdk.SessionConfigOptionCategory) *acpsdk.SessionConfigOptionCategory {
	return &category
}
