package shared

import "strings"

// PadRight pads s with spaces to visibleWidth. When the string carries ANSI
// styling, pass the unstyled length as the second arg so padding math ignores
// the escape codes; a third arg overrides the target width.
func PadRight(s string, args ...int) string {
	width := 0
	visible := len(s)
	switch len(args) {
	case 1:
		width = args[0]
	case 2:
		visible = args[0]
		width = args[1]
	}
	if pad := width - visible; pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
