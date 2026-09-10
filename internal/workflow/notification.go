package workflow

import (
	"fmt"
	"slices"
)

type NotificationEvent string

const (
	AttentionRequired NotificationEvent = "attention_required"
	RunFailed         NotificationEvent = "run_failed"
	RunSucceeded      NotificationEvent = "run_succeeded"
)

// NotificationConfig retains list presence until profile inheritance is resolved.
// A nil slice inherits; an allocated empty slice explicitly clears the field.
type NotificationConfig struct {
	Profile *string             `toml:"profile"`
	Events  []NotificationEvent `toml:"events"`
	Routes  []NotificationRoute `toml:"routes"`
}

type NotificationRoute struct {
	Destination string              `toml:"destination" json:"destination"`
	Events      []NotificationEvent `toml:"events" json:"events"`
}

// NotificationPolicy is fully resolved and contains no operator bindings.
// An absent notification table resolves to the zero (disabled) policy.
type NotificationPolicy struct {
	Events []NotificationEvent `json:"events"`
	Routes []NotificationRoute `json:"routes"`
}

func (wf *Workflow) NotificationPolicy() NotificationPolicy {
	p := wf.notificationPolicy
	p.Events = slices.Clone(p.Events)
	p.Routes = slices.Clone(p.Routes)
	for i := range p.Routes {
		p.Routes[i].Events = slices.Clone(p.Routes[i].Events)
	}
	return p
}

func ValidNotificationAlias(s string) bool { return isIdent(s) }

func validateNotificationEvents(events []NotificationEvent) error {
	for _, event := range events {
		switch event {
		case AttentionRequired, RunFailed, RunSucceeded:
		default:
			return fmt.Errorf("unknown notification event %q", event)
		}
	}
	return nil
}

func resolveNotificationPolicy(raw NotificationConfig, inherited NotificationConfig) (NotificationPolicy, error) {
	events := raw.Events
	if events == nil {
		events = inherited.Events
	}
	if events == nil {
		events = []NotificationEvent{AttentionRequired, RunFailed}
	}
	if err := validateNotificationEvents(events); err != nil {
		return NotificationPolicy{}, err
	}
	p := NotificationPolicy{}
	for _, e := range events {
		if !slices.Contains(p.Events, e) {
			p.Events = append(p.Events, e)
		}
	}
	routes := raw.Routes
	if routes == nil {
		routes = inherited.Routes
	}
	indexes := map[string]int{}
	for _, r := range routes {
		if !ValidNotificationAlias(r.Destination) {
			return NotificationPolicy{}, fmt.Errorf("invalid notification destination alias %q", r.Destination)
		}
		selected := r.Events
		if selected == nil {
			selected = p.Events
		}
		if err := validateNotificationEvents(selected); err != nil {
			return NotificationPolicy{}, err
		}
		for _, e := range selected {
			if !slices.Contains(p.Events, e) {
				return NotificationPolicy{}, fmt.Errorf("notification route events must be a subset of policy events")
			}
		}
		i, ok := indexes[r.Destination]
		if !ok {
			i = len(p.Routes)
			indexes[r.Destination] = i
			p.Routes = append(p.Routes, NotificationRoute{Destination: r.Destination})
		}
		for _, e := range selected {
			if !slices.Contains(p.Routes[i].Events, e) {
				p.Routes[i].Events = append(p.Routes[i].Events, e)
			}
		}
	}
	// Canonical policy order also makes overlapping routes independent of their order.
	for i := range p.Routes {
		var ordered []NotificationEvent
		for _, e := range p.Events {
			if slices.Contains(p.Routes[i].Events, e) {
				ordered = append(ordered, e)
			}
		}
		p.Routes[i].Events = ordered
	}
	return p, nil
}

func (wf *Workflow) resolveNotification(baseDir string) error {
	profiles, err := loadNotificationProfiles(baseDir)
	if err != nil {
		return err
	}
	if wf.Notification == nil {
		return nil
	}
	var inherited NotificationConfig
	if wf.Notification.Profile != nil {
		id := *wf.Notification.Profile
		var ok bool
		inherited, ok = profiles[id]
		if !ok {
			return fmt.Errorf("unknown notification profile %q", id)
		}
	}
	wf.notificationPolicy, err = resolveNotificationPolicy(*wf.Notification, inherited)
	return err
}
