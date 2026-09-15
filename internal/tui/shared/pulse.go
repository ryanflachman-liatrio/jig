package shared

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// gatePulsePeriod is the full breathing cycle for the human-input-gate
// attention pulse: slow and soft by design (the gate should draw the eye
// back, not read as a hard blink or strobe).
const gatePulsePeriod = 1600 * time.Millisecond

// gatePulseMaxIntensity caps how far the pulse travels toward the attention
// color so the brightest frame still reads as a gentle glow.
const gatePulseMaxIntensity = 0.65

// GatePulsePhase returns a smooth 0..1 triangle wave locked to wall-clock
// time, so every caller rendering the same instant sees the same phase
// without a shared timer or channel.
func GatePulsePhase(now time.Time) float64 {
	period := gatePulsePeriod.Milliseconds()
	pos := now.UnixMilli() % period
	t := float64(pos) / float64(period)
	if t < 0.5 {
		return t * 2
	}
	return (1 - t) * 2
}

// GateAttentionColor blends the gate label color toward the attention accent
// (Tang) by the current pulse phase, for chrome that should draw the
// operator's eye back to a pending human-input gate they are not currently
// viewing.
func GateAttentionColor(now time.Time) color.Color {
	t := GatePulsePhase(now) * gatePulseMaxIntensity
	return lerpHexColor(hexCharple, hexTang, t)
}

func lerpHexColor(a, b string, t float64) color.Color {
	ar, ag, ab := hexRGB(a)
	br, bg, bb := hexRGB(b)
	r := lerpByte(ar, br, t)
	g := lerpByte(ag, bg, t)
	bl := lerpByte(ab, bb, t)
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, bl))
}

func lerpByte(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t)
}

func hexRGB(s string) (uint8, uint8, uint8) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return 0, 0, 0
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}
