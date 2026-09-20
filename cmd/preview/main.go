// Command preview renders sample challenge artwork to PNG files. It exists so
// that changes to the procedural generators can be eyeballed without starting
// the service.
//
//	go run ./cmd/preview -out ./preview
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"

	"avestura.dev/persian-captcha/internal/challenge"
	"avestura.dev/persian-captcha/internal/imagegen"
)

func main() {
	out := flag.String("out", "preview", "directory to write PNG files into")
	seed := flag.Uint64("seed", 7, "random seed")
	level := flag.String("level", "normal", "difficulty: easy, normal or hard")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(*seed, 0x9E3779B97F4A7C15))

	writeBytes := func(name string, data []byte) {
		p := filepath.Join(*out, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", p)
	}
	writeImage := func(name string, img image.Image) {
		data, err := imagegen.EncodePNG(img)
		if err != nil {
			log.Fatal(err)
		}
		writeBytes(name, data)
	}

	// One sample of every background style.
	for _, style := range imagegen.Styles() {
		writeImage(fmt.Sprintf("bg-%s.png", style),
			imagegen.BackgroundStyle(rng, 320, 200, style, imagegen.RandomPalette(rng)))
	}

	// The icon set, on a neutral sheet.
	kinds := imagegen.ShapeKinds()
	sheet := image.NewRGBA(image.Rect(0, 0, 6*70, 2*70))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{C: color.RGBA{28, 30, 38, 255}}, image.Point{}, draw.Src)
	for i, k := range kinds {
		cx := float64(i%6)*70 + 35
		cy := float64(i/6)*70 + 35
		imagegen.FillPath(sheet, imagegen.ShapePath(k, cx, cy, 26, 0), color.RGBA{238, 226, 180, 255}, 1)
	}
	writeImage("shapes.png", sheet)

	// Every challenge kind, rendered exactly as the service would serve it,
	// with the private state dumped alongside for checking the grader.
	for _, kind := range append(challenge.VisualKinds(), challenge.KindAccessible) {
		st, err := challenge.New(kind, challenge.Level(*level), rng.Uint64())
		if err != nil {
			log.Fatal(err)
		}
		for _, part := range st.Assets() {
			data, err := st.Render(part)
			if err != nil {
				log.Fatalf("%s/%s: %v", kind, part, err)
			}
			writeBytes(fmt.Sprintf("%s-%s.png", kind, part), data)
		}
		state, _ := json.MarshalIndent(st, "", "  ")
		spec, err := st.Spec()
		if err != nil {
			log.Fatal(err)
		}
		public, _ := json.MarshalIndent(spec, "", "  ")
		writeBytes(fmt.Sprintf("%s.state.json", kind), state)
		writeBytes(fmt.Sprintf("%s.spec.json", kind), public)
	}
}
