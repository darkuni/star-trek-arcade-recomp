// Command shot runs Star Trek headless and writes vector-rendered PNGs.
//
//	shot <roms> <outdir> [-w 1600] [-h 1200] [-clean] [-coin t] [-start t] t1 t2 ...
package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"startrek/internal/assets"
	"startrek/internal/board"
	"startrek/internal/vecrender"
)

func main() {
	w := flag.Int("w", 1600, "width")
	h := flag.Int("h", 1200, "height")
	clean := flag.Bool("clean", false, "clean (no glow) look")
	coin := flag.Float64("coin", -1, "insert a coin at this time")
	start := flag.Float64("start", -1, "press start at this time")
	spin := flag.Int("spin", 0, "spinner movement per frame after start")
	flag.Parse()
	args := flag.Args()
	rom, err := assets.Load(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := args[1]
	os.MkdirAll(out, 0o755)
	var times []float64
	for _, a := range args[2:] {
		t, _ := strconv.ParseFloat(a, 64)
		times = append(times, t)
	}
	sort.Float64s(times)
	p := vecrender.Defaults()
	if *clean {
		p = vecrender.Clean()
	}
	b := board.New(rom)
	var lists [][]vecrender.Segment
	b.Frame = func(s []vecrender.Segment) {
		lists = append(lists, append([]vecrender.Segment(nil), s...))
		if len(lists) > 6 {
			lists = lists[1:]
		}
	}
	b.Reset()
	step := uint64(board.CPUHz / 60)
	tnow := 0.0
	for i, t := range times {
		for tnow < t {
			b.Ctl.Coin1 = *coin >= 0 && tnow >= *coin && tnow < *coin+0.15
			b.Ctl.Start1 = *start >= 0 && tnow >= *start && tnow < *start+0.2
			if *start >= 0 && tnow > *start+1 {
				b.Ctl.Spin = *spin
			}
			b.RunCycles(step)
			tnow += 1.0 / 60
		}
		r := vecrender.NewSoft(*w, *h, p)
		img := r.Draw(nil)
		nseg := 0
		for _, l := range lists[max(0, len(lists)-3):] {
			img = r.Draw(l)
			nseg = len(l)
		}
		name := filepath.Join(out, fmt.Sprintf("shot%02d_%05.1fs.png", i, t))
		f, _ := os.Create(name)
		png.Encode(f, img)
		f.Close()
		fmt.Printf("%s  frames %d  segments %d  pc=%04x\n", name, b.Frames, nseg, b.CPU.PC)
	}
}
