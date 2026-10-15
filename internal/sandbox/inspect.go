package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// BindingData contains only the completed deployment identity; callers receive
// copies. Snapshot validation must not use this as policy authority.
type BindingData struct {
	Endpoint       EndpointSummary `json:"endpoint"`
	DaemonID       string          `json:"daemon_id"`
	API            string          `json:"api"`
	NativePlatform string          `json:"native_platform"`
}
type Capabilities struct {
	Memory        *bool  `json:"memory,omitempty"`
	Swap          *bool  `json:"swap,omitempty"`
	CPUPeriod     *bool  `json:"cpu_period,omitempty"`
	CPUQuota      *bool  `json:"cpu_quota,omitempty"`
	PIDs          *bool  `json:"pids,omitempty"`
	CgroupVersion string `json:"cgroup_version,omitempty"`
	CgroupDriver  string `json:"cgroup_driver,omitempty"`
	Rootless      *bool  `json:"rootless,omitempty"`
	Ready         bool   `json:"reported_ready"`
	Enforcement   string `json:"enforcement"`
}
type ImageObservation struct {
	Reference string `json:"reference"`
	ID        string `json:"id,omitempty"`
	Platform  string `json:"platform,omitempty"`
	Status    string `json:"status"`
}
type StepObservation struct {
	StepID       string `json:"step_id"`
	Profile      string `json:"profile"`
	Runtime      string `json:"runtime"`
	Origin       string `json:"origin"`
	Platform     string `json:"platform,omitempty"`
	NanoCPUs     int64  `json:"nano_cpus,omitempty"`
	MemoryBytes  int64  `json:"memory_bytes,omitempty"`
	PIDs         int64  `json:"pids,omitempty"`
	CPUSource    string `json:"cpu_source,omitempty"`
	MemorySource string `json:"memory_source,omitempty"`
	PIDsSource   string `json:"pids_source,omitempty"`
	Reason       string `json:"reason,omitempty"`
}
type Inspection struct {
	Binding      *BindingData       `json:"binding,omitempty"`
	Capabilities *Capabilities      `json:"capabilities,omitempty"`
	Images       []ImageObservation `json:"images,omitempty"`
	Steps        []StepObservation  `json:"steps,omitempty"`
	Requests     int                `json:"requests"`
}

// Inspect only makes read-only metadata requests under one ten-second budget.
// expected pins a previously completed binding; identity drift never rebinds.
func Inspect(ctx context.Context, endpoint *Endpoint, policy *Policy, steps []workflow.Step, expected *BindingData) (Inspection, error) {
	result := Inspection{}
	if err := endpoint.CheckBound(); err != nil {
		return result, err
	}
	if err := policy.CheckGuarantees(); err != nil {
		return result, err
	}
	if !policy.HasDocker() {
		return result, policy.failure("sandbox_policy_denied", "docker", "")
	}
	if err := authorizeEndpointPath(endpoint.SocketPath(), policy); err != nil {
		return result, err
	}
	var images []string
	seen := map[string]bool{}
	for i := range steps {
		st := &steps[i]
		effective, err := policy.Evaluate(st, nil, nil, nil)
		if err != nil {
			return result, err
		}
		r := effective.Request
		obs := StepObservation{StepID: Display(st.ID), Profile: Display(r.Reference), Origin: r.Origin, Runtime: "host"}
		if r.Profile != nil {
			obs.Runtime = "docker"
			obs.Platform = string(r.Profile.Platform)
			obs.NanoCPUs = effective.NanoCPUs
			obs.MemoryBytes = effective.MemoryBytes
			obs.PIDs = effective.PIDs
			obs.CPUSource = effective.CPUSource
			obs.MemorySource = effective.MemorySource
			obs.PIDsSource = effective.PIDsSource
			ref := r.Profile.NormalizedImage
			if !seen[ref] {
				seen[ref] = true
				images = append(images, ref)
			}
		}
		result.Steps = append(result.Steps, obs)
	}
	if expected != nil && expected.Endpoint.URI != endpoint.summary.URI {
		return result, endpointFailure("sandbox_endpoint_changed", "binding")
	}
	ctx, cancel := context.WithTimeout(ctx, inspectionTimeout)
	defer cancel()
	g := newInspectionTransport(endpoint, images[:min(len(images), imageLimit)])
	defer g.base.CloseIdleConnections()
	httpClient := &http.Client{Transport: g, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect denied") }}
	sdk, err := client.New(client.WithHost("unix://"+endpoint.SocketPath()), client.WithHTTPClient(httpClient), client.WithTraceProvider(tracenoop.NewTracerProvider()), client.WithTraceOptions(otelhttp.WithMeterProvider(metricnoop.NewMeterProvider())))
	if err != nil {
		return result, endpointFailure("sandbox_client_unavailable", "client")
	}
	defer sdk.Close()
	failure := func(reason string) (Inspection, error) {
		result.Requests = g.requests
		if g.reason != "" {
			reason = g.reason
		}
		return result, endpointFailure(reason, "inspection")
	}
	ping, err := sdk.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return failure("sandbox_daemon_unavailable")
	}
	if _, ok := apiMinor(ping.APIVersion); !ok || !supportedAPI(sdk.ClientVersion()) {
		return failure("sandbox_api_unsupported")
	}
	version, err := sdk.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return failure("sandbox_daemon_unavailable")
	}
	maxAPI, maxOK := apiMinor(version.APIVersion)
	minAPI, minOK := apiMinor(version.MinAPIVersion)
	negotiated, _ := apiMinor(sdk.ClientVersion())
	if !maxOK || !minOK || negotiated < minAPI || negotiated > maxAPI {
		return failure("sandbox_api_unsupported")
	}
	info, err := sdk.Info(ctx, client.InfoOptions{})
	if err != nil {
		return failure("sandbox_daemon_unavailable")
	}
	if info.Info.OSType != "linux" || version.Os != "linux" || strings.TrimSpace(info.Info.ID) == "" || len(info.Info.ID) > 512 || strings.IndexFunc(info.Info.ID, unicode.IsControl) >= 0 {
		return failure("sandbox_daemon_unsupported")
	}
	native := nativePlatform(info.Info.OSType, info.Info.Architecture, "")
	if native == "" {
		return failure("sandbox_platform_unavailable")
	}
	if expected != nil && (expected.DaemonID != info.Info.ID || expected.NativePlatform != native || expected.API != sdk.ClientVersion()) {
		return failure("sandbox_endpoint_changed")
	}
	result.Binding = &BindingData{Endpoint: endpoint.Summary(), DaemonID: Display(info.Info.ID), API: sdk.ClientVersion(), NativePlatform: native}
	presence := g.presence
	cap := &Capabilities{Memory: presence.Memory, Swap: presence.Swap, CPUPeriod: presence.CPUPeriod, CPUQuota: presence.CPUQuota, PIDs: presence.PIDs, CgroupVersion: Display(info.Info.CgroupVersion), CgroupDriver: Display(info.Info.CgroupDriver), Enforcement: "unverified"}
	if presence.SecurityOptions != nil {
		rootless := false
		for _, option := range *presence.SecurityOptions {
			if option == "name=rootless" {
				rootless = true
			}
		}
		cap.Rootless = &rootless
	}
	cap.Ready = isTrue(cap.Memory) && isTrue(cap.Swap) && isTrue(cap.CPUPeriod) && isTrue(cap.CPUQuota) && isTrue(cap.PIDs) && cap.Rootless != nil && (cap.CgroupVersion == "1" || cap.CgroupVersion == "2") && cap.CgroupDriver != ""
	if isTrue(cap.Rootless) && (cap.CgroupVersion != "2" || cap.CgroupDriver != "systemd") {
		cap.Ready = false
	}
	result.Capabilities = cap
	for i := range result.Steps {
		obs := &result.Steps[i]
		if obs.Runtime != "docker" {
			continue
		}
		if obs.Platform == "" {
			obs.Platform = native
		}
		if obs.Platform != native && !(obs.Platform == "linux/arm64/v8" && native == "linux/arm64") {
			if !policy.AllowsEmulation() {
				obs.Reason = "sandbox_emulation_denied"
			} else {
				obs.Reason = "sandbox_emulation_unverified"
			}
		}
	}
	for i, ref := range images {
		observation := ImageObservation{Reference: Display(ref), Status: "uninspected"}
		if i < imageLimit {
			image, err := sdk.ImageInspect(ctx, ref)
			if err != nil {
				observation.Status = "unavailable"
				if errdefs.IsNotFound(err) {
					observation.Status = "acquisition_required"
					for j := range steps {
						r := steps[j].ResolvedSandbox()
						if r.Profile != nil && r.Profile.NormalizedImage == ref && r.Profile.Pull == "never" {
							observation.Status = "cache_required_missing"
						}
					}
				}
			} else {
				observation.Status = "cached_unverified"
				observation.ID = Display(image.ID)
				observation.Platform = nativePlatform(image.Os, image.Architecture, image.Variant)
				if observation.ID == "" || observation.Platform == "" {
					observation.Status = "metadata_unavailable"
				}
			}
		}
		result.Images = append(result.Images, observation)
	}
	result.Requests = g.requests
	if g.failed {
		return failure(g.reason)
	}
	if ctx.Err() != nil {
		return result, endpointFailure("sandbox_inspection_cancelled", "inspection")
	}
	if err := endpoint.CheckBound(); err != nil {
		return result, err
	}
	return result, nil
}
func isTrue(v *bool) bool { return v != nil && *v }
func nativePlatform(os, arch, variant string) string {
	if os != "linux" {
		return ""
	}
	switch arch {
	case "x86_64":
		arch = "amd64"
	case "aarch64":
		arch = "arm64"
	case "armv7l":
		arch = "arm"
		variant = "v7"
	case "armv6l":
		arch = "arm"
		variant = "v6"
	}
	switch arch {
	case "amd64", "arm64", "386", "ppc64le", "s390x", "riscv64":
		if variant != "" && !(arch == "arm64" && variant == "v8") {
			return ""
		}
		return "linux/" + arch
	case "arm":
		if variant == "v6" || variant == "v7" {
			return "linux/arm/" + variant
		}
	}
	return ""
}
