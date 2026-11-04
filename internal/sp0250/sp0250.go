// Package sp0250 is the GI SP0250 LPC speech synthesizer, ported from MAME's
// sp0250.cpp (Olivier Galibert): a 15-byte parameter FIFO, pitch / noise
// excitation and six 2-pole filter stages, producing one 7-bit sample per
// frame (clock / 2 / 156).
package sp0250

var gcTable = [128]uint16{
	0, 9, 17, 25, 33, 41, 49, 57, 65, 73, 81, 89, 97, 105, 113, 121,
	129, 137, 145, 153, 161, 169, 177, 185, 193, 201, 209, 217, 225, 233, 241, 249,
	257, 265, 273, 281, 289, 297, 301, 305, 309, 313, 317, 321, 325, 329, 333, 337,
	341, 345, 349, 353, 357, 361, 365, 369, 373, 377, 381, 385, 389, 393, 397, 401,
	405, 409, 413, 417, 421, 425, 427, 429, 431, 433, 435, 437, 439, 441, 443, 445,
	447, 449, 451, 453, 455, 457, 459, 461, 463, 465, 467, 469, 471, 473, 475, 477,
	479, 481, 482, 483, 484, 485, 486, 487, 488, 489, 490, 491, 492, 493, 494, 495,
	496, 497, 498, 499, 500, 501, 502, 503, 504, 505, 506, 507, 508, 509, 510, 511,
}

func ga(v byte) uint16 { return uint16(v&0x1f) << (v >> 5) }
func gc(v byte) int16 {
	r := int16(gcTable[v&0x7f])
	if v&0x80 == 0 {
		r = -r
	}
	return r
}

type filter struct{ F, B, z1, z2 int16 }

func (f *filter) apply(in int16) int16 {
	z0 := in + int16((int32(f.z1)*int32(f.F))>>8) + int16((int32(f.z2)*int32(f.B))>>9)
	f.z2 = f.z1
	f.z1 = z0
	return z0
}

// SP0250 is one chip.
type SP0250 struct {
	fifo    [15]byte
	fifoPos int
	filt    [6]filter
	amp     uint16
	pitch   byte
	repeat  byte
	voiced  bool
	lfsr    uint16
	pcount  byte
	rcount  byte
}

// New returns a reset chip.
func New() *SP0250 {
	s := &SP0250{lfsr: 0x7fff}
	s.load()
	return s
}

func (s *SP0250) load() {
	f := &s.fifo
	s.filt[0].B, s.filt[0].F = gc(f[0]), gc(f[1])
	s.amp = ga(f[2])
	s.filt[1].B, s.filt[1].F = gc(f[3]), gc(f[4])
	s.pitch = f[5]
	s.filt[2].B, s.filt[2].F = gc(f[6]), gc(f[7])
	s.repeat = f[8] & 0x3f
	s.voiced = f[8]&0x40 != 0
	s.filt[3].B, s.filt[3].F = gc(f[9]), gc(f[10])
	s.filt[4].B, s.filt[4].F = gc(f[11]), gc(f[12])
	s.filt[5].B, s.filt[5].F = gc(f[13]), gc(f[14])
	s.fifoPos = 0
	s.pcount, s.rcount = 0, 0
	for i := range s.filt {
		s.filt[i].z1, s.filt[i].z2 = 0, 0
	}
}

// Write puts a byte in the FIFO.
func (s *SP0250) Write(v byte) {
	if s.fifoPos != 15 {
		s.fifo[s.fifoPos] = v
		s.fifoPos++
	}
}

// DRQ is the data request output (true = wants data).
func (s *SP0250) DRQ() bool { return s.fifoPos != 15 }

// Next produces one sample (-64..63).
func (s *SP0250) Next() int {
	if s.rcount >= s.repeat {
		if s.fifoPos == 15 {
			s.load()
		} else {
			s.repeat = 1
			s.pcount, s.rcount = 0, 0
		}
	}
	s.lfsr ^= (s.lfsr ^ (s.lfsr >> 1)) << 15
	s.lfsr >>= 1
	var z0 int16
	if s.voiced {
		if s.pcount == 0 {
			z0 = int16(s.amp)
		}
	} else if s.lfsr&1 != 0 {
		z0 = int16(s.amp)
	} else {
		z0 = -int16(s.amp)
	}
	for i := range s.filt {
		z0 = s.filt[i].apply(z0)
	}
	dac := int(z0) >> 6
	if dac < -64 {
		dac = -64
	}
	if dac > 63 {
		dac = 63
	}
	if s.pcount == s.pitch {
		s.pcount = 0
		s.rcount++
	} else {
		s.pcount++
	}
	return dac
}
