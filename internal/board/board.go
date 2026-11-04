// Package board is the Sega G80 vector main board as configured for Star
// Trek (MAME segag80v.cpp, Aaron Giles): Z80 with the 315-0064 security chip
// (scrambles the low address byte of LD (nn),A writes), 2 wait states per
// memory access, the vector generator, the 8x8 multiplier, mangled DIP/input
// ports, spinner, coin and 40 Hz vector interrupts.
package board

import (
	"startrek/internal/assets"
	"startrek/internal/segasound"
	"startrek/internal/vecgen"
	"startrek/internal/vecrender"
	"startrek/internal/z80"
)

// Timing.
const (
	CPUHz      = vecgen.VectorClock / 4    // 3.86712 MHz
	IRQCycles  = vecgen.IRQDivider * 3 / 4 // 40 Hz refresh = 96678 CPU cycles
	waitStates = 2
)

// Inputs are the player controls.
type Inputs struct {
	Coin1, Coin2, Service, Test bool
	Start1, Start2              bool
	Fire, Photon, Impulse, Warp bool // BUTTON1..4 order is fixed below
	Spin                        int  // spinner movement since last frame (signed)
}

// Board is the main board.
type Board struct {
	ROM    *assets.ROMs
	CPU    *z80.CPU
	Gen    *vecgen.Gen
	RAM    [0x800]byte  // $c800-$cfff
	VRAM   [0x1000]byte // $e000-$efff
	USBRAM [0x1000]byte // $d000-$dfff, Universal Sound Board program RAM

	Ctl        Inputs
	DSW1, DSW2 byte // D3D2 / D1D0 (MAME port layout, before mangling)
	FlipY      bool

	scrambledPC uint16
	multData    [2]byte
	multResult  uint16
	spinSelect  byte
	spinSign    byte
	spinCount   byte
	coinFF      byte
	coinLast    byte
	edgeFF      byte
	drawEnd     uint64
	nextIRQ     uint64
	prevService bool
	irqLine     bool

	// Sound boards (Universal Sound Board + Speech Board).
	Snd     *segasound.Sound
	nextSnd uint64

	// Frame receives each refresh's strokes.
	Frame  func(segs []vecrender.Segment)
	Frames int
	segs   []vecrender.Segment
}

// New builds the board.
func New(r *assets.ROMs) *Board {
	b := &Board{ROM: r, DSW1: 0x8d, DSW2: 0x33, FlipY: true, scrambledPC: 0xffff, coinLast: 0xff}
	b.Gen = vecgen.New(r.PROMs[:])
	b.CPU = &z80.CPU{Bus: b}
	b.Snd = segasound.New(CPUHz, &b.USBRAM, r.SpeechCPU[:], r.SpeechData[:])
	b.CPU.OnFetch = func(pc uint16, op uint8) {
		b.CPU.Cycles += waitStates // opcode space tap (program tap counted in Read)
		if op == 0x32 {
			b.scrambledPC = pc
		} else {
			b.scrambledPC = 0xffff
		}
	}
	return b
}

// Reset is power-on.
func (b *Board) Reset() {
	b.CPU.Reset()
	b.nextIRQ = b.CPU.Cycles + IRQCycles
}

// decrypt64 is the 315-0064 security chip.
func decrypt64(pc uint16, lo byte) byte {
	b := uint32(lo)
	var i uint32
	switch pc & 3 {
	case 0:
		i = b
	case 1:
		i = b & 0x03
		i += (b & 0x80) >> 1
		i += (b & 0x60) >> 3
		i += ^b & 0x10
		i += (b & 0x08) << 2
		i += (b & 0x04) << 5
	case 2:
		i = b & 0x03
		i += (b & 0x80) >> 4
		i += (^b & 0x40) >> 1
		i += (b & 0x20) >> 1
		i += (b & 0x10) >> 2
		i += (b & 0x08) << 3
		i += (b & 0x04) << 5
	case 3:
		i = b & 0x23
		i += (b & 0xc0) >> 4
		i += (b & 0x10) << 2
		i += (b & 0x08) << 1
		i += (^b & 0x04) << 5
	}
	return byte(i)
}

func (b *Board) decryptOffset(off uint16) uint16 {
	if b.scrambledPC == 0xffff {
		return off
	}
	pc := b.scrambledPC
	b.scrambledPC = 0xffff
	return off&0xff00 | uint16(decrypt64(pc, byte(off)))
}

// Read implements z80.Bus.
func (b *Board) Read(a uint16) uint8 {
	b.CPU.Cycles += waitStates
	switch {
	case a < 0xc000:
		return b.ROM.Main[a]
	case a >= 0xc800 && a < 0xd000:
		return b.RAM[a-0xc800]
	case a >= 0xd000 && a < 0xe000:
		return b.USBRAM[a-0xd000]
	case a >= 0xe000 && a < 0xf000:
		return b.VRAM[a-0xe000]
	}
	return 0xff
}

// Write implements z80.Bus.
func (b *Board) Write(a uint16, v uint8) {
	b.CPU.Cycles += waitStates
	switch {
	case a >= 0xc800 && a < 0xd000:
		b.RAM[b.decryptOffset(a-0xc800)&0x7ff] = v
	case a >= 0xd000 && a < 0xe000:
		off := b.decryptOffset(a - 0xd000)
		b.Snd.RunTo(b.CPU.Cycles)
		b.Snd.USB.RAMWrite(off&0xfff, v)
	case a >= 0xe000 && a < 0xf000:
		b.VRAM[b.decryptOffset(a-0xe000)&0xfff] = v
	}
}

func bit(v byte, n uint) byte { return (v >> n) & 1 }

func (b *Board) portD7D6() byte {
	v := byte(0xff)
	if b.Ctl.Coin1 {
		v &^= 0x01
	}
	if b.Ctl.Coin2 {
		v &^= 0x10
	}
	if b.CPU.Cycles >= b.drawEnd {
		v &^= 0x20 // DRAW is active high while the generator is busy
	}
	return v
}

func (b *Board) portD5D4() byte {
	v := byte(0xff)
	if b.Ctl.Service {
		v &^= 0x01
	}
	return v
}

func (b *Board) portFC() byte {
	var v byte
	in := &b.Ctl
	if in.Start1 {
		v |= 0x01
	}
	if in.Start2 {
		v |= 0x02
	}
	if in.Photon {
		v |= 0x04 // BUTTON2
	}
	if in.Fire {
		v |= 0x08 // BUTTON1
	}
	if in.Impulse {
		v |= 0x10 // BUTTON3
	}
	if in.Warp {
		v |= 0x20 // BUTTON4
	}
	return v
}

// In implements z80.Bus (I/O space, 8-bit ports).
func (b *Board) In(port uint16) uint8 {
	p := byte(port)
	switch {
	case p == 0xbe:
		r := byte(b.multResult)
		b.multResult >>= 8
		return r
	case p >= 0xf8 && p <= 0xfb:
		o := uint(p & 3)
		d76, d54, d32, d10 := b.portD7D6(), b.portD5D4(), b.DSW1, b.DSW2
		return bit(d76, o)<<7 | bit(d76, o+4)<<6 | bit(d54, o)<<5 | bit(d54, o+4)<<4 |
			bit(d32, o)<<3 | bit(d32, o+4)<<2 | bit(d10, o)<<1 | bit(d10, o+4)
	case p == 0xfc:
		if b.spinSelect&1 != 0 {
			return b.portFC()
		}
		return ^(b.spinCount<<1 | b.spinSign)
	case p == 0x3f:
		b.CPU.Cycles += 200 // MAME: status reads stall the CPU
		b.Snd.RunTo(b.CPU.Cycles)
		return b.Snd.USB.Status()
	}
	return 0xff
}

// Out implements z80.Bus.
func (b *Board) Out(port uint16, v uint8) {
	p := byte(port)
	switch {
	case p == 0xbd || p == 0xbe:
		b.multData[p-0xbd] = v
		if p == 0xbe {
			b.multResult = uint16(b.multData[0]) * uint16(b.multData[1])
		}
	case p == 0xf8:
		b.spinSelect = v
	case p == 0x38:
		b.Snd.RunTo(b.CPU.Cycles)
		b.Snd.Speech.Write(v)
	case p == 0x3b:
		b.Snd.RunTo(b.CPU.Cycles)
		b.Snd.Control(v)
	case p == 0x3f:
		b.Snd.RunTo(b.CPU.Cycles)
		b.Snd.USB.Data(v)
	}
}

func (b *Board) updateInt() {
	if b.coinFF != 0 || b.edgeFF != 0 {
		b.irqLine = true
	} else {
		b.irqLine = false
	}
	b.coinFF &^= 0x04
}

// coin edge flip-flops (inputs are active low: a press is a 1 -> 0 edge).
func (b *Board) coinW(idx uint, pressed bool) {
	mask := byte(1) << idx
	state := !pressed
	if !state && b.coinLast&mask != 0 {
		b.coinFF |= mask
	} else {
		b.coinFF &^= mask
	}
	if state {
		b.coinLast |= mask
	} else {
		b.coinLast &^= mask
	}
	b.updateInt()
}

// Frame-rate input sampling: coins, service NMI, spinner.
func (b *Board) sampleInputs() {
	b.coinW(0, b.Ctl.Coin1)
	b.coinW(1, b.Ctl.Coin2)
	b.coinW(2, b.Ctl.Service)
	if b.Ctl.Test && !b.prevService {
		b.CPU.NMI()
	}
	b.prevService = b.Ctl.Test
	if d := b.Ctl.Spin; d != 0 {
		if d < 0 {
			b.spinSign = 1
			b.spinCount += byte(-d)
		} else {
			b.spinSign = 0
			b.spinCount += byte(d)
		}
		b.Ctl.Spin = 0
	}
}

// RunCycles runs the main CPU for n cycles.
func (b *Board) RunCycles(n uint64) {
	b.sampleInputs()
	end := b.CPU.Cycles + n
	for b.CPU.Cycles < end {
		if b.irqLine {
			if b.CPU.IRQ(0xff) {
				b.coinFF, b.edgeFF = 0, 0 // IRQ acknowledge
				b.updateInt()
			}
		}
		b.CPU.Step()
		if b.CPU.Cycles >= b.nextSnd {
			b.nextSnd = b.CPU.Cycles + 400 // ~100 us slices, like MAME's USB interleave
			b.Snd.RunTo(b.CPU.Cycles)
		}
		if b.CPU.Cycles >= b.nextIRQ {
			b.nextIRQ += IRQCycles
			b.refresh()
		}
	}
}

func (b *Board) refresh() {
	b.edgeFF = 1
	b.updateInt()
	var used int
	b.segs, used = b.Gen.Frame(b.VRAM[:], b.segs, b.FlipY)
	b.drawEnd = b.CPU.Cycles + uint64(used)*2/3 // VCL is 1.5x the CPU clock
	b.Frames++
	if b.Frame != nil {
		b.Frame(b.segs)
	}
}
