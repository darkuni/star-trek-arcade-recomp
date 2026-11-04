// Package mcs48 is an Intel 8035/8048 (MCS-48) core, ported from MAME's
// mcs48.cpp (Dan Boris, Mirko Buffoni, Aaron Giles, Chris Kirmse): opcode
// semantics, PSW / stack, timer & counter, external and timer interrupts.
// No UPI-41 or 8243 expander features (not used by the Sega boards).
//
// Time is counted in machine cycles (input clock / 15).
package mcs48

const (
	fC = 0x80
	fA = 0x40
	fF = 0x20
	fB = 0x10

	timerEnabled   = 1
	counterEnabled = 2
)

// Bus is the CPU's view of the world.
type Bus interface {
	Program(a uint16) byte   // program memory (12-bit)
	ExtRead(a byte) byte     // MOVX A,@Rr (I/O space)
	ExtWrite(a byte, v byte) // MOVX @Rr,A
	PortRead(p int) byte     // P1 / P2 pins (BUS when p == 0)
	PortWrite(p int, v byte) // P1 / P2 latches (BUS when p == 0)
	Test(t int) bool         // T0 / T1
}

// CPU is the register file.
type CPU struct {
	Bus Bus

	PC     uint16
	prevPC uint16
	A      byte
	PSW    byte
	RAM    [64]byte
	P1, P2 byte
	bus    byte
	f1     bool
	a11    uint16

	timer       byte
	prescaler   byte
	t1History   byte
	timeCount   byte
	timerFlag   bool
	timerOvf    bool
	tirqEnabled bool
	xirqEnabled bool
	irqInProg   bool
	irqPolled   bool

	// IRQ is the /INT input (true = asserted).
	IRQ bool

	// Cycles counts machine cycles.
	Cycles uint64
}

// Reset is the RESET pin.
func (c *CPU) Reset() {
	c.PC = 0
	c.PSW &= fC | fA
	c.f1 = false
	c.a11 = 0
	c.tirqEnabled, c.xirqEnabled = false, false
	c.timeCount = 0
	c.timerFlag = false
	c.irqInProg, c.timerOvf, c.irqPolled = false, false, false
	c.bus = 0xff
	c.Bus.PortWrite(0, 0xff)
	c.P1, c.P2 = 0xff, 0xff
	c.Bus.PortWrite(1, c.P1)
	c.Bus.PortWrite(2, c.P2)
}

func (c *CPU) regBase() int {
	if c.PSW&fB != 0 {
		return 24
	}
	return 0
}
func (c *CPU) r(n int) *byte       { return &c.RAM[c.regBase()+n] }
func (c *CPU) ramR(a byte) byte    { return c.RAM[a&0x3f] }
func (c *CPU) ramW(a byte, v byte) { c.RAM[a&0x3f] = v }
func (c *CPU) prog(a uint16) byte  { return c.Bus.Program(a & 0xfff) }
func (c *CPU) testT(t int) bool    { return c.Bus.Test(t) }
func (c *CPU) incPC()              { c.PC = (c.PC+1)&0x7ff | c.PC&0x800 }
func (c *CPU) fetch() byte         { v := c.prog(c.PC); c.incPC(); return v }

func (c *CPU) burn(n int) {
	if c.timeCount != 0 {
		over := false
		if c.timeCount&timerEnabled != 0 {
			old := c.timer
			ps := int(c.prescaler) + n
			c.timer += byte(ps >> 5)
			c.prescaler = byte(ps & 0x1f)
			over = c.timer < old
		} else if c.timeCount&counterEnabled != 0 {
			for i := 0; i < n; i++ {
				c.t1History <<= 1
				if c.testT(1) {
					c.t1History |= 1
				}
				if c.t1History&3 == 2 {
					c.timer++
					if c.timer == 0 {
						over = true
					}
				}
			}
		}
		if over {
			c.timerFlag = true
			if c.tirqEnabled {
				c.timerOvf = true
			}
		}
	}
	c.Cycles += uint64(n)
}

func (c *CPU) pushPCPSW() {
	sp := c.PSW & 7
	c.ramW(8+2*sp, byte(c.PC))
	c.ramW(9+2*sp, byte(c.PC>>8)&0x0f|c.PSW&0xf0)
	c.PSW = c.PSW&0xf0 | (sp+1)&7
}

func (c *CPU) pullPCPSW() {
	sp := (c.PSW - 1) & 7
	pc := uint16(c.ramR(8+2*sp)) | uint16(c.ramR(9+2*sp))<<8
	c.PSW = byte(pc>>8)&0xf0 | sp
	if c.irqInProg {
		pc &= 0x7ff
	} else {
		pc &= 0xfff
	}
	c.PC = pc
}

func (c *CPU) pullPC() {
	sp := (c.PSW - 1) & 7
	pc := uint16(c.ramR(8+2*sp)) | uint16(c.ramR(9+2*sp))<<8
	if c.irqInProg {
		pc &= 0x7ff
	} else {
		pc &= 0xfff
	}
	c.PC = pc
	c.PSW = c.PSW&0xf0 | sp
}

func (c *CPU) add(d byte, carry bool) {
	ci := uint16(0)
	if carry && c.PSW&fC != 0 {
		ci = 1
	}
	t := uint16(c.A) + uint16(d) + ci
	t4 := uint16(c.A&0x0f) + uint16(d&0x0f) + ci
	c.PSW &^= fC | fA
	c.PSW |= byte(t4<<2) & fA
	c.PSW |= byte(t>>1) & fC
	c.A = byte(t)
}

func (c *CPU) jmp(addr uint16) {
	a11 := c.a11
	if c.irqInProg {
		a11 = 0
	}
	c.PC = addr | a11
}

func (c *CPU) call(addr uint16) { c.pushPCPSW(); c.jmp(addr) }

func (c *CPU) jcc(cond bool) {
	pch := c.PC & 0xf00
	off := c.fetch()
	if cond {
		c.PC = pch | uint16(off)
	}
}

func (c *CPU) portR(p int) byte { return c.Bus.PortRead(p) }
func (c *CPU) portW(p int, v byte) {
	c.Bus.PortWrite(p, v)
}

func (c *CPU) checkIRQ() {
	if c.irqInProg {
		return
	}
	if c.IRQ && c.xirqEnabled {
		c.burn(2)
		c.irqInProg = true
		if c.irqPolled {
			c.PC = (c.prevPC+1)&0x7ff | c.prevPC&0x800
			c.jcc(true)
		}
		c.call(0x03)
	} else if c.timerOvf && c.tirqEnabled {
		c.burn(2)
		c.irqInProg = true
		c.call(0x07)
		c.timerOvf = false
	}
}

// Step runs one instruction (and any pending interrupt), returning machine cycles.
func (c *CPU) Step() int {
	start := c.Cycles
	c.checkIRQ()
	c.irqPolled = false
	c.prevPC = c.PC
	op := c.fetch()
	c.exec(op)
	return int(c.Cycles - start)
}

func (c *CPU) exec(op byte) {
	n := int(op & 7)
	switch op {
	// --- arithmetic / logic with registers (low 3 bits select Rn) ---
	case 0x68, 0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f:
		c.burn(1)
		c.add(*c.r(n), false)
	case 0x60, 0x61:
		c.burn(1)
		c.add(c.ramR(*c.r(n & 1)), false)
	case 0x03:
		c.burn(2)
		c.add(c.fetch(), false)
	case 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f:
		c.burn(1)
		c.add(*c.r(n), true)
	case 0x70, 0x71:
		c.burn(1)
		c.add(c.ramR(*c.r(n & 1)), true)
	case 0x13:
		c.burn(2)
		c.add(c.fetch(), true)
	case 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x5f:
		c.burn(1)
		c.A &= *c.r(n)
	case 0x50, 0x51:
		c.burn(1)
		c.A &= c.ramR(*c.r(n & 1))
	case 0x53:
		c.burn(2)
		c.A &= c.fetch()
	case 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f:
		c.burn(1)
		c.A |= *c.r(n)
	case 0x40, 0x41:
		c.burn(1)
		c.A |= c.ramR(*c.r(n & 1))
	case 0x43:
		c.burn(2)
		c.A |= c.fetch()
	case 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf:
		c.burn(1)
		c.A ^= *c.r(n)
	case 0xd0, 0xd1:
		c.burn(1)
		c.A ^= c.ramR(*c.r(n & 1))
	case 0xd3:
		c.burn(2)
		c.A ^= c.fetch()

	// --- ports / bus ---
	case 0x98:
		c.burn(2)
		c.bus = c.portR(0) & c.fetch()
		c.portW(0, c.bus)
	case 0x99:
		c.burn(2)
		c.P1 &= c.fetch()
		c.portW(1, c.P1)
	case 0x9a:
		c.burn(2)
		c.P2 &= c.fetch()
		c.portW(2, c.P2)
	case 0x88:
		c.burn(2)
		c.bus = c.portR(0) | c.fetch()
		c.portW(0, c.bus)
	case 0x89:
		c.burn(2)
		c.P1 |= c.fetch()
		c.portW(1, c.P1)
	case 0x8a:
		c.burn(2)
		c.P2 |= c.fetch()
		c.portW(2, c.P2)
	case 0x02:
		c.burn(2)
		c.bus = c.A
		c.portW(0, c.A)
	case 0x39:
		c.burn(2)
		c.P1 = c.A
		c.portW(1, c.P1)
	case 0x3a:
		c.burn(2)
		c.P2 = c.A
		c.portW(2, c.P2)
	case 0x08:
		c.burn(2)
		c.A = c.portR(0)
	case 0x09:
		c.burn(2)
		c.A = c.portR(1) & c.P1
	case 0x0a:
		c.burn(2)
		c.A = c.portR(2) & c.P2

	// --- calls / jumps ---
	case 0x14, 0x34, 0x54, 0x74, 0x94, 0xb4, 0xd4, 0xf4:
		c.burn(2)
		c.call(uint16(c.fetch()) | uint16(op>>5)<<8)
	case 0x04, 0x24, 0x44, 0x64, 0x84, 0xa4, 0xc4, 0xe4:
		c.burn(2)
		c.jmp(uint16(c.fetch()) | uint16(op>>5)<<8)
	case 0x12, 0x32, 0x52, 0x72, 0x92, 0xb2, 0xd2, 0xf2:
		c.burn(2)
		c.jcc(c.A&(1<<(op>>5)) != 0)
	case 0xf6:
		c.burn(2)
		c.jcc(c.PSW&fC != 0)
	case 0xe6:
		c.burn(2)
		c.jcc(c.PSW&fC == 0)
	case 0xb6:
		c.burn(2)
		c.jcc(c.PSW&fF != 0)
	case 0x76:
		c.burn(2)
		c.jcc(c.f1)
	case 0x86:
		c.burn(2)
		c.irqPolled = !c.IRQ
		c.jcc(c.IRQ)
	case 0x26:
		c.burn(2)
		c.jcc(!c.testT(0))
	case 0x36:
		c.burn(2)
		c.jcc(c.testT(0))
	case 0x46:
		c.burn(2)
		c.jcc(!c.testT(1))
	case 0x56:
		c.burn(2)
		c.jcc(c.testT(1))
	case 0x96:
		c.burn(2)
		c.jcc(c.A != 0)
	case 0xc6:
		c.burn(2)
		c.jcc(c.A == 0)
	case 0x16:
		c.burn(2)
		f := c.timerFlag
		c.jcc(f)
		c.timerFlag = false
	case 0xe8, 0xe9, 0xea, 0xeb, 0xec, 0xed, 0xee, 0xef:
		c.burn(2)
		r := c.r(n)
		*r--
		c.jcc(*r != 0)
	case 0xb3:
		c.burn(2)
		c.PC &= 0xf00
		c.PC |= uint16(c.prog(c.PC | uint16(c.A)))
	case 0x83:
		c.burn(2)
		c.pullPC()
	case 0x93:
		c.burn(2)
		c.irqInProg = false
		c.pullPCPSW()

	// --- accumulator ---
	case 0x27:
		c.burn(1)
		c.A = 0
	case 0x37:
		c.burn(1)
		c.A ^= 0xff
	case 0x17:
		c.burn(1)
		c.A++
	case 0x07:
		c.burn(1)
		c.A--
	case 0x57:
		c.burn(1)
		if c.A&0x0f > 9 || c.PSW&fA != 0 {
			if c.A > 0xf9 {
				c.PSW |= fC
			}
			c.A += 6
		}
		if c.A&0xf0 > 0x90 || c.PSW&fC != 0 {
			c.A += 0x60
			c.PSW |= fC
		}
	case 0x47:
		c.burn(1)
		c.A = c.A<<4 | c.A>>4
	case 0xe7:
		c.burn(1)
		c.A = c.A<<1 | c.A>>7
	case 0xf7:
		c.burn(1)
		nc := c.A & fC
		c.A = c.A<<1 | c.PSW>>7
		c.PSW = c.PSW&^fC | nc
	case 0x77:
		c.burn(1)
		c.A = c.A>>1 | c.A<<7
	case 0x67:
		c.burn(1)
		nc := (c.A << 7) & fC
		c.A = c.A>>1 | c.PSW&fC
		c.PSW = c.PSW&^fC | nc

	// --- flags ---
	case 0x97:
		c.burn(1)
		c.PSW &^= fC
	case 0xa7:
		c.burn(1)
		c.PSW ^= fC
	case 0x85:
		c.burn(1)
		c.PSW &^= fF
	case 0x95:
		c.burn(1)
		c.PSW ^= fF
	case 0xa5:
		c.burn(1)
		c.f1 = false
	case 0xb5:
		c.burn(1)
		c.f1 = !c.f1

	// --- registers ---
	case 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f:
		c.burn(1)
		*c.r(n)++
	case 0x10, 0x11:
		c.burn(1)
		a := *c.r(n & 1)
		c.ramW(a, c.ramR(a)+1)
	case 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf:
		c.burn(1)
		*c.r(n)--
	case 0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f:
		c.burn(1)
		r := c.r(n)
		c.A, *r = *r, c.A
	case 0x20, 0x21:
		c.burn(1)
		a := *c.r(n & 1)
		t := c.ramR(a)
		c.ramW(a, c.A)
		c.A = t
	case 0x30, 0x31:
		c.burn(1)
		a := *c.r(n & 1)
		old := c.ramR(a)
		c.ramW(a, old&0xf0|c.A&0x0f)
		c.A = c.A&0xf0 | old&0x0f

	// --- moves ---
	case 0x23:
		c.burn(2)
		c.A = c.fetch()
	case 0xc7:
		c.burn(1)
		c.A = c.PSW | 0x08
	case 0xd7:
		c.burn(1)
		c.PSW = c.A &^ 0x08
	case 0xf8, 0xf9, 0xfa, 0xfb, 0xfc, 0xfd, 0xfe, 0xff:
		c.burn(1)
		c.A = *c.r(n)
	case 0xf0, 0xf1:
		c.burn(1)
		c.A = c.ramR(*c.r(n & 1))
	case 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf:
		c.burn(1)
		*c.r(n) = c.A
	case 0xa0, 0xa1:
		c.burn(1)
		c.ramW(*c.r(n & 1), c.A)
	case 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf:
		c.burn(2)
		*c.r(n) = c.fetch()
	case 0xb0, 0xb1:
		c.burn(2)
		a := *c.r(n & 1)
		c.ramW(a, c.fetch())
	case 0x42:
		c.burn(1)
		c.A = c.timer
	case 0x62:
		c.burn(1)
		c.timer = c.A
	case 0xa3:
		c.burn(2)
		c.A = c.prog(c.PC&0xf00 | uint16(c.A))
	case 0xe3:
		c.burn(2)
		c.A = c.prog(0x300 | uint16(c.A))
	case 0x80, 0x81:
		c.burn(2)
		c.A = c.Bus.ExtRead(*c.r(n & 1))
	case 0x90, 0x91:
		c.burn(2)
		c.Bus.ExtWrite(*c.r(n & 1), c.A)

	// --- interrupts / timer / banks ---
	case 0x05:
		c.burn(1)
		c.xirqEnabled = true
	case 0x15:
		c.burn(1)
		c.xirqEnabled = false
	case 0x25:
		c.burn(1)
		c.tirqEnabled = true
	case 0x35:
		c.burn(1)
		c.tirqEnabled = false
		c.timerOvf = false
	case 0x45:
		c.burn(1)
		if c.timeCount&counterEnabled == 0 {
			c.t1History = 0
			if c.testT(1) {
				c.t1History = 1
			}
		}
		c.timeCount = counterEnabled
	case 0x55:
		c.burn(1)
		c.timeCount = timerEnabled
		c.prescaler = 0
	case 0x65:
		c.burn(1)
		c.timeCount = 0
	case 0x75: // ENT0 CLK - no T0 clock output used
		c.burn(1)
	case 0xc5:
		c.burn(1)
		c.PSW &^= fB
	case 0xd5:
		c.burn(1)
		c.PSW |= fB
	case 0xe5:
		c.burn(1)
		c.a11 = 0
	case 0xf5:
		c.burn(1)
		c.a11 = 0x800
	case 0x00:
		c.burn(1)
	default: // illegal (and 8243 expander ops, unused)
		c.burn(1)
	}
}
