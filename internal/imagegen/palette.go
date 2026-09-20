package imagegen

import (
	"image/color"
	"math"
	"math/rand/v2"
)

// Palette is an ordered set of colours used by one background.
type Palette struct {
	Name   string
	Base   color.RGBA   // deepest background tone
	Tones  []color.RGBA // mid-ground washes
	Accent color.RGBA   // line work and motifs
	Light  color.RGBA   // highlights
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

// palettes are drawn from Persian tilework, miniature painting and carpet
// dyes: lapis and turquoise, saffron and pomegranate, desert ochres.
var palettes = []Palette{
	{
		Name:   "lajevard", // lapis lazuli
		Base:   rgb(16, 32, 74),
		Tones:  []color.RGBA{rgb(26, 60, 128), rgb(18, 92, 140), rgb(38, 46, 104)},
		Accent: rgb(232, 196, 88),
		Light:  rgb(158, 214, 228),
	},
	{
		Name:   "firouzeh", // turquoise
		Base:   rgb(10, 74, 82),
		Tones:  []color.RGBA{rgb(24, 128, 130), rgb(46, 160, 150), rgb(16, 100, 116)},
		Accent: rgb(242, 222, 180),
		Light:  rgb(168, 226, 214),
	},
	{
		Name:   "zaferan", // saffron
		Base:   rgb(96, 44, 16),
		Tones:  []color.RGBA{rgb(162, 88, 28), rgb(196, 128, 40), rgb(128, 60, 32)},
		Accent: rgb(250, 226, 166),
		Light:  rgb(238, 178, 96),
	},
	{
		Name:   "anaar", // pomegranate
		Base:   rgb(74, 16, 32),
		Tones:  []color.RGBA{rgb(138, 28, 52), rgb(168, 52, 70), rgb(96, 24, 56)},
		Accent: rgb(244, 214, 176),
		Light:  rgb(226, 132, 128),
	},
	{
		Name:   "sabz", // garden green
		Base:   rgb(18, 58, 40),
		Tones:  []color.RGBA{rgb(34, 102, 66), rgb(62, 132, 78), rgb(24, 84, 68)},
		Accent: rgb(238, 232, 180),
		Light:  rgb(154, 200, 140),
	},
	{
		Name:   "kavir", // desert
		Base:   rgb(70, 54, 40),
		Tones:  []color.RGBA{rgb(134, 106, 74), rgb(168, 140, 100), rgb(104, 82, 62)},
		Accent: rgb(248, 238, 214),
		Light:  rgb(206, 178, 134),
	},
	{
		Name:   "shabaneh", // night
		Base:   rgb(22, 24, 38),
		Tones:  []color.RGBA{rgb(44, 48, 78), rgb(62, 58, 96), rgb(32, 40, 62)},
		Accent: rgb(204, 210, 236),
		Light:  rgb(122, 134, 184),
	},
	{
		Name:   "banafsh", // violet
		Base:   rgb(44, 24, 72),
		Tones:  []color.RGBA{rgb(84, 46, 128), rgb(112, 66, 156), rgb(62, 36, 102)},
		Accent: rgb(238, 214, 246),
		Light:  rgb(176, 140, 214),
	},
}

// RandomPalette picks one of the built-in palettes.
func RandomPalette(rng *rand.Rand) Palette {
	return palettes[rng.IntN(len(palettes))]
}

// PaletteByName returns a palette by name, falling back to the first one.
func PaletteByName(name string) Palette {
	for _, p := range palettes {
		if p.Name == name {
			return p
		}
	}
	return palettes[0]
}

// PaletteNames lists the available palettes.
func PaletteNames() []string {
	out := make([]string, len(palettes))
	for i, p := range palettes {
		out[i] = p.Name
	}
	return out
}

// Tone returns one of the palette mid-tones.
func (p Palette) Tone(i int) color.RGBA {
	if len(p.Tones) == 0 {
		return p.Base
	}
	return p.Tones[((i%len(p.Tones))+len(p.Tones))%len(p.Tones)]
}

// lerpColor mixes two colours; t == 0 yields a, t == 1 yields b.
func lerpColor(a, b color.RGBA, t float64) color.RGBA {
	t = clamp(t, 0, 1)
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
