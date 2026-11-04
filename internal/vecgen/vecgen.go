// Package vecgen is the Sega G80 vector generator, ported from MAME's
// segag80v_v.cpp (Aaron Giles). Each refresh it walks the symbol list in
// vector RAM: a symbol is a position, a pointer to a vector list, a rotation
// angle and a scale; each vector is a length, an angle (looked up in the
// sine-table PROM) and a 6-bit colour. Output is beam strokes in normalized
// tube coordinates - never pixels.
package vecgen

import "startrek/internal/vecrender"

// Clocks (Hz).
const (
	VectorClock = 15468480
	U34Clock    = VectorClock / 3
	VCLClock    = U34Clock / 2 // one beam step
	IRQDivider  = 0x1f788
	// Budget per refresh, in VCL steps (40 Hz frame).
	frameVCL = VCLClock * IRQDivider / U34Clock // = IRQDivider / 2
	u51VCL   = 16                               // U51 phase clock = VCL / 16
)

// Visible area of the 10-bit DACs (MAME: x 512..1536, y 608..1440 in its
// shifted space, i.e. DAC x 0..1023, y 96..928).
const (
	minX, maxX = 0, 1023
	minY, maxY = 96, 928
)

// Gen holds the sine table PROM.
type Gen struct {
	sin []byte // 1K, every other byte used
}

// New makes a generator from the PROM region (sine table at $000).
func New(proms []byte) *Gen { return &Gen{sin: proms[:0x400]} }

func adjust(rawx, rawy uint16) (x, y int, clipped bool) {
	ox := int((rawx & 0x7ff) ^ 0x200)
	oy := int((rawy & 0x7ff) ^ 0x200)
	switch ox & 0x600 {
	case 0x200:
		ox, clipped = 0, true
	case 0x400:
		ox, clipped = 0x3ff, true
	default:
		ox &= 0x3ff
	}
	switch oy & 0x600 {
	case 0x200:
		oy, clipped = 0, true
	case 0x400:
		oy, clipped = 0x3ff, true
	default:
		oy &= 0x3ff
	}
	return ox, oy, clipped
}

var pal2 = [4]float32{0, 0x55 / 255.0, 0xaa / 255.0, 1}

type point struct {
	x, y    int
	r, g, b float32
	lit     bool
}

// Frame generates one refresh from vector RAM (4K). It returns the strokes
// and how many VCL steps the drawing took (the DRAW busy flag stays set that
// long). flipY matches the cabinet orientation.
func (v *Gen) Frame(vram []byte, out []vecrender.Segment, flipY bool) ([]vecrender.Segment, int) {
	out = out[:0]
	budget := frameVCL
	rd := func(a *uint16) byte { b := vram[*a&0xfff]; *a++; return b }
	var pts []point
	add := func(x, y int, r, g, b float32, lit bool) { pts = append(pts, point{x, y, r, g, b, lit}) }
	var sym uint16
	for budget > 0 {
		draw := rd(&sym)
		curx := uint16(rd(&sym))
		curx |= uint16(rd(&sym)&7) << 8
		curx |= (curx << 1) & 0x800
		cury := uint16(rd(&sym))
		cury |= uint16(rd(&sym)&7) << 8
		cury |= (cury << 1) & 0x800
		vec := uint16(rd(&sym))
		vec |= uint16(rd(&sym)&0xf) << 8
		symAngle := uint16(rd(&sym))
		symAngle |= uint16(rd(&sym)&3) << 8
		scale := uint16(rd(&sym))
		budget -= 10 * u51VCL
		if draw&1 != 0 {
			x, y, clipped := adjust(curx, cury)
			if !clipped {
				add(x, y, 0, 0, 0, false)
			}
			for budget > 0 {
				attrib := rd(&vec)
				length := (uint16(rd(&vec)) * scale) >> 7
				vecAngle := uint16(rd(&vec))
				vecAngle |= uint16(rd(&vec)&3) << 8
				dx := uint16(v.sin[((vecAngle+symAngle)&0x1ff)<<1])
				dy := uint16(v.sin[((vecAngle+symAngle+0x100)&0x1ff)<<1])
				budget -= 4 * u51VCL
				c := (attrib >> 1) & 0x3f
				r, g, b := pal2[(c>>4)&3], pal2[(c>>2)&3], pal2[c&3]
				lit := attrib&1 != 0 && c != 0
				x, y, clipped = adjust(curx, cury)
				var xa, ya uint16
				for ; length != 0 && budget > 0; length-- {
					xa += dx + (dx >> 7)
					if (vecAngle+symAngle)&0x200 == 0 {
						curx += xa >> 8
					} else {
						curx -= xa >> 8
					}
					xa &= 0xff
					ya += dy + (dy >> 7)
					if (vecAngle+symAngle+0x100)&0x200 == 0 {
						cury += ya >> 8
					} else {
						cury -= ya >> 8
					}
					ya &= 0xff
					nx, ny, nclip := adjust(curx, cury)
					if nclip != clipped {
						if !nclip {
							add(nx, ny, 0, 0, 0, false)
						} else {
							add(nx, ny, r, g, b, lit)
						}
					}
					x, y, clipped = nx, ny, nclip
					budget--
				}
				if !clipped {
					add(x, y, r, g, b, lit)
				}
				if attrib&0x80 != 0 {
					break
				}
			}
		}
		if draw&0x80 != 0 {
			break
		}
	}
	// Points to strokes (a lit point is drawn from the previous point).
	const w, h = float32(maxX - minX + 1), float32(maxY - minY + 1)
	for i := 1; i < len(pts); i++ {
		p := pts[i]
		if !p.lit {
			continue
		}
		q := pts[i-1]
		y0, y1 := float32(q.y-minY)/h, float32(p.y-minY)/h
		if flipY {
			y0, y1 = 1-y0, 1-y1
		}
		out = append(out, vecrender.Segment{
			X0: float32(q.x-minX) / w, Y0: y0, X1: float32(p.x-minX) / w, Y1: y1,
			R: p.r, G: p.g, B: p.b,
		})
	}
	return out, frameVCL - budget
}
