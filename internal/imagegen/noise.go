package imagegen

import (
	"math"
	"math/rand/v2"
)

// noise2D is a seeded value-noise field with smooth interpolation. It gives
// the backgrounds organic texture so that a solver cannot rely on flat,
// perfectly predictable regions when locating the puzzle notch.
type noise2D struct {
	perm [512]int
	grad [256]float64
}

func newNoise(rng *rand.Rand) *noise2D {
	n := &noise2D{}
	var p [256]int
	for i := range p {
		p[i] = i
	}
	rng.Shuffle(len(p), func(i, j int) { p[i], p[j] = p[j], p[i] })
	for i := range n.perm {
		n.perm[i] = p[i&255]
	}
	for i := range n.grad {
		n.grad[i] = rng.Float64()*2 - 1
	}
	return n
}

func (n *noise2D) value(xi, yi int) float64 {
	h := n.perm[(n.perm[xi&255]+yi)&511]
	return n.grad[h&255]
}

// smoothstep-like fade curve, 6t^5 - 15t^4 + 10t^3.
func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

// at samples the field at (x, y), returning a value in roughly [-1, 1].
func (n *noise2D) at(x, y float64) float64 {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	u, v := fade(fx), fade(fy)

	v00 := n.value(x0, y0)
	v10 := n.value(x0+1, y0)
	v01 := n.value(x0, y0+1)
	v11 := n.value(x0+1, y0+1)

	a := v00 + u*(v10-v00)
	b := v01 + u*(v11-v01)
	return a + v*(b-a)
}

// fbm sums several octaves of the field for a more natural texture.
func (n *noise2D) fbm(x, y float64, octaves int, lacunarity, gain float64) float64 {
	var sum, amp, norm float64 = 0, 1, 0
	freq := 1.0
	for range octaves {
		sum += amp * n.at(x*freq, y*freq)
		norm += amp
		freq *= lacunarity
		amp *= gain
	}
	if norm == 0 {
		return 0
	}
	return sum / norm
}
