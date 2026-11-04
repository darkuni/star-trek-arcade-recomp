package segasound

import (
	"math"

	"startrek/internal/mcs48"
)

// Universal Sound Board (MAME segausb.cpp, Aaron Giles, non-netlist model):
// an 8035 running a program the main CPU uploads into 4K RAM, three 8253
// timer groups each with three channels, an MM5837 noise source, envelope
// DACs and the RC filter network, all stepped at 2 MHz.

const (
	usbMaster   = 6000000
	usbCPU      = usbMaster / 15 // machine cycles per second
	usb2MHz     = usbMaster / 3
	usbPCS      = usb2MHz / 2
	usbGOS      = usb2MHz / 16 / 4
	mm5837Clock = 100000
	t1Period    = 256 // 2 MHz clocks per T1 counter tick
)

type rc struct{ cap, exp float64 }

func (f *rc) configure(r, c float64) {
	f.cap = 0
	f.exp = 1 - math.Exp(-1/(r*c*usb2MHz))
}
func (f *rc) stepRC(in float64) float64 { f.cap += (in - f.cap) * f.exp; return f.cap }
func (f *rc) stepCR(in float64) float64 {
	r := in - f.cap
	f.cap += r * f.exp
	return r
}

type pitChan struct {
	holding     bool
	latchMode   byte
	latchToggle byte
	clockMode   byte
	output      byte
	lastGate    byte
	gate        byte
	count       uint16
	remain      uint16
	subcount    int
}

func (ch *pitChan) clock() {
	old := ch.lastGate
	ch.lastGate = ch.gate
	if ch.holding {
		return
	}
	switch ch.clockMode {
	case 1:
		if old == 0 && ch.gate != 0 {
			ch.output = 0
			ch.remain = ch.count
		} else {
			ch.remain--
			if ch.remain == 0 {
				ch.output = 1
			}
		}
	case 3:
		ch.remain = (ch.remain - 1) &^ 1
		if ch.remain == 0 {
			ch.output ^= 1
			ch.remain = ch.count
		}
	}
}

type timerGroup struct {
	ch         [3]pitChan
	env        [3]float64
	chanFilter [2]rc
	gate1      rc
	gate2      rc
	config     byte
}

// USB is the Universal Sound Board.
type USB struct {
	CPU      *mcs48.CPU
	PgmRAM   *[0x1000]byte
	work     [0x400]byte
	bank     int
	inLatch  byte
	outLatch byte
	lastP2   byte
	t1Clock  uint32
	t1Sub    int
	reset    bool // CPU held in reset

	groups      [3]timerGroup
	noiseShift  uint32
	noiseState  byte
	noiseSub    int
	gateRC1     [2]float64
	gateRC2     [2]float64
	finalFilter rc
	noiseF      [5]rc
}

func newUSB(pgm *[0x1000]byte) *USB {
	u := &USB{PgmRAM: pgm, reset: true, noiseShift: 0x15555}
	u.CPU = &mcs48.CPU{Bus: u}
	for i := range u.groups {
		g := &u.groups[i]
		g.chanFilter[0].configure(10e3, 1e-6)
		g.chanFilter[1].configure(10e3, 1e-6)
		g.gate1.configure(100e3, 0.01e-6)
		g.gate2.configure(2*100e3, 0.01e-6)
	}
	var t rc
	t.configure(100e3, 0.01e-6)
	u.gateRC1[0] = t.exp
	t.configure(1e3, 0.01e-6)
	u.gateRC1[1] = t.exp
	t.configure(2*100e3, 0.01e-6)
	u.gateRC2[0] = t.exp
	t.configure(2*1e3, 0.01e-6)
	u.gateRC2[1] = t.exp
	u.noiseF[0].configure(2.7e3+2.7e3, 1.0e-6)
	u.noiseF[1].configure(2.7e3+1e3, 0.30e-6)
	u.noiseF[2].configure(2.7e3+270, 0.15e-6)
	u.noiseF[3].configure(2.7e3+0, 0.082e-6)
	u.noiseF[4].configure(33e3, 0.1e-6)
	u.finalFilter.configure(100e3, 4.7e-6)
	return u
}

// --- host side ---

// Status is a main CPU read of port $3f.
func (u *USB) Status() byte { return u.outLatch&0x81 | u.inLatch&0x7e }

// Data is a main CPU write to port $3f.
func (u *USB) Data(v byte) {
	hold := v&0x80 != 0
	if u.reset && !hold {
		u.CPU.Reset()
	}
	u.reset = hold
	if u.lastP2&0x40 == 0 {
		v &^= 0x7f
	}
	u.inLatch = v
}

// RAMWrite is a main CPU write into the program RAM ($d000-$dfff).
func (u *USB) RAMWrite(off uint16, v byte) {
	if u.inLatch&0x80 != 0 {
		u.PgmRAM[off&0xfff] = v
	}
}

// --- 8035 side ---

func (u *USB) Program(a uint16) byte { return u.PgmRAM[a&0xfff] }
func (u *USB) ExtRead(a byte) byte   { return u.work[u.bank*256+int(a)] }
func (u *USB) ExtWrite(a byte, v byte) {
	off := u.bank*256 + int(a)
	u.work[off] = v
	switch off &^ 3 {
	case 0x00:
		u.timerW(0, off&3, v)
	case 0x04:
		u.envW(0, off&3, v)
	case 0x08:
		u.timerW(1, off&3, v)
	case 0x0c:
		u.envW(1, off&3, v)
	case 0x10:
		u.timerW(2, off&3, v)
	case 0x14:
		u.envW(2, off&3, v)
	}
}
func (u *USB) PortRead(p int) byte {
	if p == 1 {
		return u.inLatch & 0x7f
	}
	return 0xff
}
func (u *USB) PortWrite(p int, v byte) {
	switch p {
	case 1:
		u.outLatch = u.outLatch&0xfe | v>>7
	case 2:
		old := u.lastP2
		u.lastP2 = v
		u.bank = int(v & 3)
		u.outLatch = (v&0x40)<<1 | u.outLatch&0x7f
		if v&0x40 == 0 {
			u.inLatch = 0
		}
		if old&0x80 != 0 && v&0x80 == 0 {
			u.t1Clock = 0
		}
	}
}
func (u *USB) Test(t int) bool {
	if t == 1 {
		return u.t1Clock&0x10 != 0
	}
	return false
}

func (u *USB) timerW(which, off int, v byte) {
	g := &u.groups[which]
	switch off {
	case 0, 1, 2:
		ch := &g.ch[off]
		was := ch.holding
		switch ch.latchMode {
		case 1:
			ch.count = uint16(v)
			ch.holding = false
		case 2:
			ch.count = uint16(v) << 8
			ch.holding = false
		case 3:
			if ch.latchToggle == 0 {
				ch.count = ch.count&0xff00 | uint16(v)
				ch.latchToggle = 1
			} else {
				ch.count = ch.count&0x00ff | uint16(v)<<8
				ch.holding = false
				ch.latchToggle = 0
			}
		}
		if was && !ch.holding {
			ch.remain = 1
		}
	case 3:
		if (v>>6)&3 < 3 {
			ch := &g.ch[(v>>6)&3]
			ch.holding = true
			ch.latchMode = (v >> 4) & 3
			ch.clockMode = (v >> 1) & 7
			ch.latchToggle = 0
			if ch.clockMode == 1 {
				ch.output = 1
			} else {
				ch.output = 0
			}
		}
	}
}

func (u *USB) envW(which, off int, v byte) {
	g := &u.groups[which]
	if off < 3 {
		g.env[off] = float64(v)
	} else {
		g.config = v & 1
	}
}

// sample advances one 2 MHz clock and returns the board output.
func (u *USB) sample() float64 {
	// T1 counter (U33): 2 MHz / 256, held clear by P2 bit 7.
	if u.t1Sub++; u.t1Sub >= t1Period {
		u.t1Sub = 0
		if u.lastP2&0x80 == 0 {
			u.t1Clock++
		}
	}
	// Noise source.
	if u.noiseSub--; u.noiseSub < 0 {
		u.noiseShift = u.noiseShift<<1 | ((u.noiseShift>>13)^(u.noiseShift>>16))&1
		u.noiseState = byte((u.noiseShift >> 16) & 1)
		u.noiseSub += usb2MHz / mm5837Clock
	}
	ns := float64(u.noiseState)
	f := &u.noiseF
	f[0].cap = 0.99765*f[0].cap + ns*0.0990460
	f[1].cap = 0.96300*f[1].cap + ns*0.2965164
	f[2].cap = 0.57000*f[2].cap + ns*1.0526913
	noise := f[0].cap + f[1].cap + f[2].cap + ns*0.1848
	noise = f[4].stepCR(noise) * 0.075

	var out float64
	for gi := range u.groups {
		g := &u.groups[gi]
		for c := 0; c < 2; c++ {
			ch := &g.ch[c]
			if ch.subcount--; ch.subcount < 0 {
				ch.subcount += usb2MHz / usbPCS
				ch.gate = 1
				ch.clock()
			}
		}
		chan0 := g.chanFilter[0].stepCR(float64(g.ch[0].output)) * g.env[0] / 100
		chan1 := g.chanFilter[1].stepCR(float64(g.ch[1].output)) * g.env[1] / 100
		c2 := &g.ch[2]
		if c2.subcount--; c2.subcount < 0 {
			c2.subcount += usb2MHz / usbGOS / 2
			c2.gate ^= 1
		}
		c2.clock()
		g.gate1.exp = u.gateRC1[c2.output&1]
		g.gate2.exp = u.gateRC2[c2.output&1]
		var mix float64
		if g.config == 0 {
			chan2 := g.gate2.stepRC(g.gate1.stepRC(noise)) * -1.56 * g.env[2] / 33
			mix = chan0 + chan1 + chan2
		} else {
			chan2 := -noise * g.env[2] / 33
			mix = chan0 + chan1 + chan2
			mix = g.gate2.stepRC(g.gate1.stepRC(-mix)) * 1.56
		}
		out += mix
	}
	return 0.1 * u.finalFilter.stepCR(out)
}

// ResetHeld reports whether the main CPU is holding the 8035 in reset.
func (u *USB) ResetHeld() bool { return u.reset }
