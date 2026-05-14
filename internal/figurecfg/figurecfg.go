package figurecfg

import (
	"os"
	"strings"
)

// Config drives optional ASCII rendering for notification titles.
type Config struct {
	Enabled bool

	// Renderer is "gofigure" or "blocklet".
	Renderer string

	GoFigureFont string

	BlockletBin  string
	BlockletFont string
}

// FromEnv builds Config from process environment (defaults documented in README).
func FromEnv() Config {
	r := strings.ToLower(strings.TrimSpace(os.Getenv("NTFY_TITLE_FIGURE_RENDERER")))
	if r == "" {
		r = "gofigure"
	}
	return Config{
		Enabled:  envBool("NTFY_PRINT_TITLE_FIGURE"),
		Renderer: r,

		GoFigureFont: strings.TrimSpace(os.Getenv("NTFY_TITLE_FIGURE_FONT")),

		BlockletBin:  strings.TrimSpace(os.Getenv("NTFY_BLOCKLET_BIN")),
		BlockletFont: strings.TrimSpace(os.Getenv("NTFY_BLOCKLET_FONT")),
	}
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
