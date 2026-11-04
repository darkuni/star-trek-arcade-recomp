// Command wav is a development tool: it runs Star Trek headless and writes
// the sound output to a WAV file.
//
//	wav <roms> out.wav seconds [coinAt] [startAt]
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"

	"startrek/internal/assets"
	"startrek/internal/board"
	"startrek/internal/segasound"
)

func main() {
	rom, err := assets.Load(os.Args[1])
	if err != nil {
		panic(err)
	}
	secs, _ := strconv.ParseFloat(os.Args[3], 64)
	coinAt, startAt := -1.0, -1.0
	if len(os.Args) > 4 {
		coinAt, _ = strconv.ParseFloat(os.Args[4], 64)
	}
	if len(os.Args) > 5 {
		startAt, _ = strconv.ParseFloat(os.Args[5], 64)
	}
	b := board.New(rom)
	b.Reset()
	var pcm []float32
	step := uint64(board.CPUHz / 60)
	for f := 0; f < int(secs*60); f++ {
		t := float64(f) / 60
		b.Ctl.Coin1 = coinAt >= 0 && t >= coinAt && t < coinAt+0.15
		b.Ctl.Start1 = startAt >= 0 && t >= startAt && t < startAt+0.2
		b.RunCycles(step)
		pcm = b.Snd.TakeSamples(pcm)
		if f%300 == 0 {
			var sum float64
			n := 0
			for _, v := range pcm[max(0, len(pcm)-48000*5):] {
				sum += float64(v * v)
				n++
			}
			u := b.Snd.USB
			fmt.Printf("t=%5.1fs rms5s=%.4f usbReset=%v usbPC=%03x spPC=%03x\n", t, math.Sqrt(sum/float64(max(n, 1))), u.ResetHeld(), u.CPU.PC, b.Snd.Speech.CPU.PC)
		}
	}
	f, _ := os.Create(os.Args[2])
	defer f.Close()
	n := len(pcm)
	for _, h := range []any{[]byte("RIFF"), uint32(36 + n*2), []byte("WAVEfmt "), uint32(16), uint16(1), uint16(1),
		uint32(segasound.SampleRate), uint32(segasound.SampleRate * 2), uint16(2), uint16(16), []byte("data"), uint32(n * 2)} {
		binary.Write(f, binary.LittleEndian, h)
	}
	buf := make([]int16, n)
	for i, v := range pcm {
		buf[i] = int16(math.Max(-1, math.Min(1, float64(v))) * 32000)
	}
	binary.Write(f, binary.LittleEndian, buf)
	fmt.Println("wrote", n, "samples")
}
