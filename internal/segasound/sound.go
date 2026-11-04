// Package segasound is Star Trek's audio: the Universal Sound Board and the
// Sega Speech Board (8035 + SP0250), mixed through the speech board's CD4053
// gates the way MAME routes them for startrek, then resampled to 48 kHz.
// Both 8035s run in step with the main Z80 by time.
package segasound

import (
	"startrek/internal/mcs48"
	"startrek/internal/sp0250"
)

// SampleRate is the mixer output rate.
const SampleRate = 48000

const (
	speechMaster = 3120000
	speechCPU    = speechMaster / 15      // 208 kHz machine cycles
	sp0250Rate   = speechMaster / 2 / 156 // 10 kHz samples
)

// Speech is the Sega Speech Board.
type Speech struct {
	CPU   *mcs48.CPU
	ROM   []byte // 2K program, mirrored
	Data  []byte // 16K speech data
	Chip  *sp0250.SP0250
	latch byte
	t0    bool
	p2    byte
}

func (s *Speech) Program(a uint16) byte   { return s.ROM[a&0x7ff] }
func (s *Speech) ExtRead(a byte) byte     { return s.Data[int(s.p2&0x3f)*0x100+int(a)] }
func (s *Speech) ExtWrite(a byte, v byte) { s.Chip.Write(v) }
func (s *Speech) PortRead(p int) byte {
	if p == 1 {
		return s.latch & 0x7f
	}
	return 0xff
}
func (s *Speech) PortWrite(p int, v byte) {
	switch p {
	case 1:
		if v&0x80 == 0 {
			s.t0 = false
		}
	case 2:
		s.p2 = v
	}
}
func (s *Speech) Test(t int) bool {
	if t == 0 {
		return s.t0
	}
	return s.Chip.DRQ()
}

// Write is the main CPU's port $38.
func (s *Speech) Write(v byte) {
	old := s.latch
	s.latch = v
	s.CPU.IRQ = v&0x80 == 0
	if old&0x80 == 0 && v&0x80 != 0 {
		s.t0 = true
	}
}

// Sound is both boards plus the mixer.
type Sound struct {
	USB    *USB
	Speech *Speech

	mainHz uint64

	speechOn, usbOn bool
	// Gains (menu): 1.0 = MAME levels.
	SpeechGain, SFXGain float32

	usbTime    uint64 // 2 MHz clocks run
	spTime     uint64 // speech CPU machine cycles run
	spSampleAt uint64 // speech CPU cycle of the next SP0250 sample (x10)
	spCur      float64

	acc   float64
	accN  int
	outPh uint64 // output phase in 2 MHz units * SampleRate
	out   []float32
	dcX   float64
	dcY   float64
}

// New builds the boards. pgm is the USB program RAM the main CPU also sees.
func New(mainHz uint64, pgm *[0x1000]byte, speechROM, speechData []byte) *Sound {
	s := &Sound{mainHz: mainHz, SpeechGain: 1, SFXGain: 1}
	s.USB = newUSB(pgm)
	s.Speech = &Speech{ROM: speechROM, Data: speechData, Chip: sp0250.New()}
	s.Speech.CPU = &mcs48.CPU{Bus: s.Speech}
	s.Speech.CPU.Reset()
	s.Speech.CPU.IRQ = true // latch starts at 0: bit 7 low asserts /INT
	return s
}

// Control is the main CPU's port $3b (CD4053 gates).
func (s *Sound) Control(v byte) {
	s.speechOn = v&0x08 != 0
	s.usbOn = v&0x20 != 0
}

// RunTo brings both boards up to main CPU cycle t.
func (s *Sound) RunTo(t uint64) {
	// Speech board.
	spTarget := t * speechCPU / s.mainHz
	sc := s.Speech.CPU
	for sc.Cycles < spTarget {
		sc.Step()
	}
	// USB: CPU at 400 kHz machine cycles, audio at 2 MHz (5 per cycle).
	usbTarget := t * usb2MHz / s.mainHz
	uc := s.USB.CPU
	for s.usbTime < usbTarget {
		n := 5
		if !s.USB.reset {
			n = uc.Step() * 5
		}
		for i := 0; i < n; i++ {
			v := s.USB.sample()
			s.usbTime++
			// The SP0250 runs off the speech board clock: advance it by time.
			for s.spSampleAt*usb2MHz < s.usbTime*sp0250Rate {
				s.spSampleAt++
				s.spCur = float64(s.Speech.Chip.Next()) / 128
			}
			var mix float64
			if s.speechOn {
				mix += s.spCur * float64(s.SpeechGain)
			}
			if s.usbOn {
				mix += v * float64(s.SFXGain)
			}
			s.acc += mix
			s.accN++
			s.outPh += SampleRate
			if s.outPh >= usb2MHz {
				s.outPh -= usb2MHz
				o := s.acc / float64(s.accN)
				s.acc, s.accN = 0, 0
				y := o - s.dcX + 0.9995*s.dcY
				s.dcX, s.dcY = o, y
				s.out = append(s.out, float32(y))
			}
		}
	}
}

// TakeSamples returns and clears the mixed output so far.
func (s *Sound) TakeSamples(dst []float32) []float32 {
	dst = append(dst, s.out...)
	s.out = s.out[:0]
	return dst
}
