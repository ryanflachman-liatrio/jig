package shared

// Charmtone palette (crush's default "Pantera" theme), dark-only. Names mirror
// github.com/charmbracelet/x/exp/charmtone so the mapping back to upstream stays
// greppable. We keep hex strings (for glamour's ansi.StyleConfig, which wants
// *string) and derive lipgloss.Color from them for lipgloss styles.
const (
	// Brand.
	hexCharple = "#6B50FF" // primary   (violet)
	hexDolly   = "#FF60FF" // secondary (magenta)
	hexBok     = "#68FFD6" // accent
	hexBlush   = "#FF84FF" // keyword
	hexButter  = "#FFFAF1" // onPrimary (fg atop colored backgrounds)

	// Foreground ladder, brightest → dimmest.
	hexSash   = "#ECEBF0" // fgBase
	hexSmoke  = "#BFBCC8" // fgSubtle
	hexSquid  = "#858392" // fgMuted
	hexOyster = "#605F6B" // fgDim

	// Background ladder, least → most visible atop the base.
	hexPepper = "#201F26" // bgBase
	hexBBQ    = "#2D2C36" // bgLeastVisible
	hexChar   = "#3A3943" // bgLessVisible / separator
	hexIron   = "#4D4C57" // bgMostVisible

	// Status.
	hexSriracha = "#EB4268" // error
	hexCoral    = "#FF577D" // destructive
	hexMustard  = "#F5EF34" // warning
	hexTang     = "#FF985A" // attention
	hexJulep    = "#00FFB2" // success
	hexGuac     = "#12C78F" // success (subtle)
	hexMalibu   = "#00A4FF" // info
	hexCitron   = "#E8FF27" // busy
)
