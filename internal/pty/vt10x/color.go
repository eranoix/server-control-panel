package vt10x

const (
	Black Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	LightGrey
	DarkGrey
	LightRed
	LightGreen
	LightYellow
	LightBlue
	LightMagenta
	LightCyan
	White
)

const (
	DefaultFG Color = 1<<24 + iota
	DefaultBG
	DefaultCursor
)

type Color uint32

func (c Color) ANSI() bool {
	return (c < 16)
}
