package tui

// primaryBorderSeq is the SGR truecolor foreground for the Charple primary
// token (#6B50FF) that lipgloss emits for the focused panel border. Used by
// chat_test.go to assert which panel holds the primary-colored border.
const primaryBorderSeq = "\x1b[38;2;107;80;255m"

// ansiStrip removes SGR sequences so index math over visible text is meaningful.
func ansiStrip(s string) string {
	var b []byte
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			if i < len(s) {
				i++ // consume 'm'
			}
			continue
		}
		b = append(b, s[i])
		i++
	}
	return string(b)
}
