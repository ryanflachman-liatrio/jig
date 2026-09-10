package notification

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"jig/internal/workflow"
)

type ReadinessEntry struct {
	Alias   string
	Type    DestinationType
	Enabled bool
	Events  []workflow.NotificationEvent
	Status  string
}

type ReadinessReport struct {
	Events       []workflow.NotificationEvent
	Enabled      bool
	Status       string
	Destinations []ReadinessEntry
	Problem      bool
}

// Inspection provides read-only dependencies. There is deliberately no sender
// or command runner: readiness can inspect prerequisites but cannot deliver.
type Inspection struct {
	ReadFile      func(string) ([]byte, error)
	ResolveSecret func(string) (string, error)
	DesktopStatus func() string
}

func Inspect(policy workflow.NotificationPolicy, root string, deps Inspection) ReadinessReport {
	cfg, err := LoadLocalConfig(root, deps.ReadFile)
	if err != nil {
		report := ReadinessReport{Events: slices.Clone(policy.Events), Status: err.Error(), Problem: true}
		for _, route := range policy.Routes {
			report.Destinations = append(report.Destinations, ReadinessEntry{Alias: route.Destination, Events: slices.Clone(route.Events), Status: err.Error()})
		}
		return report
	}
	report, _ := ResolveBindings(policy, cfg, deps)
	return report
}

func ResolveBindings(policy workflow.NotificationPolicy, cfg LocalConfig, deps Inspection) (ReadinessReport, []Binding) {
	report := ReadinessReport{Events: slices.Clone(policy.Events), Enabled: cfg.Enabled, Status: "ready"}
	if err := cfg.validate(); err != nil {
		report.Status = err.Error()
		report.Problem = true
		return report, nil
	}
	if !cfg.Enabled {
		report.Status = "disabled"
	} else if len(policy.Events) == 0 || len(policy.Routes) == 0 {
		report.Status = "empty_policy"
	}
	dests := map[string]Destination{}
	for _, d := range cfg.Destinations {
		dests[d.ID] = d
	}
	var bindings []Binding
	for _, route := range policy.Routes {
		d, exists := dests[route.Destination]
		e := ReadinessEntry{Alias: route.Destination, Type: d.Type, Enabled: d.Enabled, Events: slices.Clone(route.Events)}
		switch {
		case !cfg.Enabled:
			e.Status = "globally_disabled"
		case len(route.Events) == 0:
			e.Status = "route_disabled"
		case !exists:
			e.Status = "binding_missing"
		case !d.Enabled:
			e.Status = "destination_disabled"
		default:
			b, status := resolveBinding(d, route.Events, deps)
			e.Status = status
			if status == "ready" {
				bindings = append(bindings, b)
			}
		}
		switch e.Status {
		case "ready", "globally_disabled", "route_disabled", "destination_disabled":
		default:
			report.Problem = true
		}
		report.Destinations = append(report.Destinations, e)
	}
	if report.Problem {
		report.Status = "not_ready"
	}
	return report, bindings
}

func resolveBinding(d Destination, events []workflow.NotificationEvent, deps Inspection) (Binding, string) {
	b := Binding{Alias: d.ID, Type: d.Type, Events: slices.Clone(events)}
	if d.Type == Desktop {
		probe := deps.DesktopStatus
		if probe == nil {
			probe = LocalDesktopStatus
		}
		status := probe()
		switch status {
		case "ready", "desktop_unsupported", "desktop_helper_missing", "desktop_session_missing":
		default:
			status = "desktop_unavailable"
		}
		return b, status
	}
	if d.URLSecret == nil || deps.ResolveSecret == nil {
		return Binding{}, "url_secret_missing"
	}
	value, err := deps.ResolveSecret(*d.URLSecret)
	if err != nil || value == "" {
		return Binding{}, "url_secret_missing"
	}
	if !validHTTPS(value) {
		return Binding{}, "url_invalid"
	}
	b.url = value
	if d.BearerSecret != nil {
		value, err = deps.ResolveSecret(*d.BearerSecret)
		if err != nil || value == "" {
			return Binding{}, "bearer_secret_missing"
		}
		// Reject controls before they could become an HTTP header value.
		if strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return Binding{}, "bearer_invalid"
		}
		b.bearer = value
	}
	return b, "ready"
}

func LocalDesktopStatus() string { return desktopStatus(runtime.GOOS, exec.LookPath, os.Getenv) }

func desktopStatus(platform string, lookPath func(string) (string, error), getenv func(string) string) string {
	switch platform {
	case "darwin":
		if _, err := lookPath("/usr/bin/osascript"); err != nil {
			return "desktop_helper_missing"
		}
	case "linux":
		if _, err := lookPath("notify-send"); err != nil {
			return "desktop_helper_missing"
		}
		if getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
			return "desktop_session_missing"
		}
	default:
		return "desktop_unsupported"
	}
	return "ready"
}

func (r ReadinessReport) Render(w io.Writer) {
	fmt.Fprintf(w, "notification policy events: %s\nnotifications enabled: %t\nstatus: %s\n", strings.Join(eventStrings(r.Events), ", "), r.Enabled, r.Status)
	if len(r.Events) == 0 {
		fmt.Fprintln(w, "policy disabled: no events")
	}
	if len(r.Destinations) == 0 {
		fmt.Fprintln(w, "policy empty: no destination routes")
	}
	for _, d := range r.Destinations {
		fmt.Fprintf(w, "destination %s: type=%s enabled=%t events=[%s] status=%s\n", d.Alias, d.Type, d.Enabled, strings.Join(eventStrings(d.Events), ", "), d.Status)
	}
	fmt.Fprintln(w, "Local readiness only; delivery is not verified and desktop visibility is not guaranteed. No notifications were sent.")
}

func eventStrings(events []workflow.NotificationEvent) []string {
	result := make([]string, len(events))
	for i, e := range events {
		result[i] = string(e)
	}
	return result
}
