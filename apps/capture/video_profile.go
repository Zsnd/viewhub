package capture

import "math"

// Starting recommendations, not predictions of bandwidth or equal perceptual
// quality across encoders. Keep one authoritative formula on the server.
func recommendedMaxBitrate(c Config) int {
	base := 6000.0 // 1080p30 H.264, high quality.
	if c.Codec == "h265" {
		base = 4000
	}
	factor := 1.0
	if c.Quality == "standard" {
		factor = 0.7
	}
	if c.Quality == "ultra" {
		factor = 1.5
	}
	rate := base * float64(c.Width) * float64(c.Height) / (1920 * 1080) * float64(c.FPS) / 30 * factor
	return max(128, min(20000, int(math.Round(rate/250))*250))
}

func qualityValue(c Config) int {
	q := 23
	if c.Quality == "standard" {
		q = 28
	}
	if c.Quality == "ultra" {
		q = 19
	}
	if c.Codec == "h265" {
		q += 2
	}
	return q
}
