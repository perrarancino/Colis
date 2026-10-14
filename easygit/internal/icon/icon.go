// Package icon draws the EasyGit logo (a green tile with a tiny commit graph),
// so the app needs no image assets.
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

type pt struct{ x, y float64 }

func distSeg(p, a, b pt) float64 {
	dx, dy := b.x-a.x, b.y-a.y
	t := ((p.x-a.x)*dx + (p.y-a.y)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.x-(a.x+t*dx), p.y-(a.y+t*dy))
}

func roundRectSDF(p pt, half, r float64) float64 {
	qx := math.Abs(p.x-0.5) - (half - r)
	qy := math.Abs(p.y-0.5) - (half - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}

// Render returns a size x size NRGBA image.
func Render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 3
	top := pt{0.34, 0.26}
	bot := pt{0.34, 0.76}
	branch := pt{0.68, 0.44}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var accR, accG, accB, accA float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					p := pt{(float64(x) + (float64(sx)+0.5)/ss) / float64(size), (float64(y) + (float64(sy)+0.5)/ss) / float64(size)}
					if roundRectSDF(p, 0.5, 0.22) > 0 {
						continue
					}
					c := [3]float64{0x1f, 0x8a, 0x3f}
					// vertical gradient
					g := 0.85 + 0.25*(1-p.y)
					c = [3]float64{c[0] * g, c[1] * g, c[2] * g}
					white := distSeg(p, top, bot) < 0.035 ||
						distSeg(p, pt{0.34, 0.62}, branch) < 0.035 ||
						math.Hypot(p.x-top.x, p.y-top.y) < 0.085 ||
						math.Hypot(p.x-bot.x, p.y-bot.y) < 0.085 ||
						math.Hypot(p.x-branch.x, p.y-branch.y) < 0.085
					if white {
						c = [3]float64{255, 255, 255}
					}
					accR += c[0]
					accG += c[1]
					accB += c[2]
					accA += 255
				}
			}
			n := float64(ss * ss)
			if accA > 0 {
				a := accA / n
				img.SetNRGBA(x, y, color.NRGBA{uint8(accR / (accA / 255)), uint8(accG / (accA / 255)), uint8(accB / (accA / 255)), uint8(a)})
			}
		}
	}
	return img
}

// BGRAPremultiplied returns pixel data in the layout Win32 icons expect.
func BGRAPremultiplied(size int) []byte {
	img := Render(size)
	out := make([]byte, size*size*4)
	for i := 0; i < size*size; i++ {
		r, g, b, a := img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3]
		out[i*4] = uint8(uint16(b) * uint16(a) / 255)
		out[i*4+1] = uint8(uint16(g) * uint16(a) / 255)
		out[i*4+2] = uint8(uint16(r) * uint16(a) / 255)
		out[i*4+3] = a
	}
	return out
}

// ICO encodes the logo as a multi-size .ico (PNG frames), for the exe resource.
func ICO(sizes ...int) []byte {
	var frames [][]byte
	for _, s := range sizes {
		var b bytes.Buffer
		_ = png.Encode(&b, Render(s))
		frames = append(frames, b.Bytes())
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, uint32(len(frames[i])))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(frames[i])
	}
	for _, f := range frames {
		out.Write(f)
	}
	return out.Bytes()
}
