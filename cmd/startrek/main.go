// Command startrek runs Sega's 1982 Star Trek (Strategic Operations
// Simulator) as a native program.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"startrek/internal/assets"
	"startrek/internal/board"
	"startrek/internal/vecrender"
	"startrek/internal/vecrender/gpu"
)

const tps = 60

type game struct {
	b   *board.Board
	gpu *gpu.GPU
	set Settings

	menu      menu
	toast     string
	toastTime int

	list     []vecrender.Segment
	listSeq  int
	drawnSeq int
	strokes  []gpu.Stroke
	frame    []gpu.Stroke

	pad      padTracker
	coinHold int
	spinFrac float64
	captured bool
	lastMX   int

	audio   *audioOut
	samples []float32
	showFPS bool
	ticks   int

	err  string
	quit bool
}

func romSearch() []string {
	var p []string
	if len(os.Args) > 1 {
		p = append(p, os.Args[1])
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		p = append(p, filepath.Join(d, "startrek.zip"), filepath.Join(d, "roms", "startrek.zip"), filepath.Join(d, "roms"))
	}
	p = append(p, "startrek.zip", filepath.Join("roms", "startrek.zip"))
	if h, err := os.UserHomeDir(); err == nil {
		p = append(p, filepath.Join(h, "startrek.zip"))
	}
	return p
}

func main() {
	g := &game{set: loadSettings()}
	rom, err := assets.Load(romSearch()...)
	if err != nil {
		var me *assets.MissingError
		if errors.As(err, &me) {
			g.err = "Star Trek ROMs not found or incomplete.\n\nPut your startrek.zip (MAME 'startrek' set) next to this program.\n\nMissing:\n  " +
				strings.Join(me.Missing, "\n  ")
		} else {
			g.err = err.Error()
		}
	} else {
		g.b = board.New(rom)
		g.b.Frame = func(s []vecrender.Segment) {
			g.list = append(g.list[:0], s...)
			g.listSeq++
		}
		g.b.Reset()
	}
	if g.gpu, err = gpu.New(g.set.Params()); err != nil {
		panic(err)
	}
	g.buildMenu()
	g.audio = newAudio()
	g.applySettings()
	ebiten.SetWindowTitle("Star Trek")
	ebiten.SetWindowSize(winSize())
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetFullscreen(g.set.Fullscreen)
	ebiten.SetTPS(tps)
	if err := ebiten.RunGame(g); err != nil && err != ebiten.Termination {
		panic(err)
	}
}

// gameRect is the 4:3 tube area inside the window, in device pixels.
func gameRect(w, h int) (x, y, gw, gh int) {
	gw, gh = w, w*3/4
	if gh > h {
		gh, gw = h, h*4/3
	}
	return (w - gw) / 2, (h - gh) / 2, gw, gh
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (g *game) say(s string) {
	g.toast = s
	g.toastTime = tps * 2
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// The mouse drives the spinner. It is captured whenever the game is playing
// and focused (hidden pointer, unlimited movement), released for the menu or
// when the window loses focus.
func (g *game) setCapture(on bool) {
	if on == g.captured {
		return
	}
	g.captured = on
	if on {
		ebiten.SetCursorMode(ebiten.CursorModeCaptured)
	} else {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
	}
	g.lastMX, _ = ebiten.CursorPosition()
}

func (g *game) Update() error {
	if g.quit {
		return ebiten.Termination
	}
	g.ticks++
	if g.toastTime > 0 {
		g.toastTime--
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF11) || (inpututil.IsKeyJustPressed(ebiten.KeyEnter) && ebiten.IsKeyPressed(ebiten.KeyAlt)) {
		g.set.Fullscreen = !g.set.Fullscreen
		g.applySettings()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF3) {
		g.showFPS = !g.showFPS
	}
	if os.Getenv("STARTREK_DEBUG") != "" && g.ticks%120 == 0 && g.b != nil {
		fmt.Printf("tick %d  game time %.1fs  TPS %.1f  FPS %.1f  frames %d\n", g.ticks, float64(g.b.CPU.Cycles)/board.CPUHz, ebiten.ActualTPS(), ebiten.ActualFPS(), g.b.Frames)
	}
	ps := g.pad.poll()
	if ps.quit {
		g.quit = true
	}

	if g.menu.open {
		g.setCapture(false)
		g.menuInput(ps)
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyTab) || ps.menuEdge {
		g.menu.open = true
		g.menu.sel = 0
		g.audio.clear()
		return nil
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyF2) {
		g.cycleLook()
		g.applySettings()
		g.say("LOOK: " + g.set.look())
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		g.set.InvertY = !g.set.InvertY
		g.applySettings()
		g.say("REVERSE SPINNER: " + onOff(g.set.InvertY))
	}
	if g.b == nil {
		return nil
	}
	// The mouse is always captured while playing (no desktop pointer over the
	// game); the menu or losing focus releases it.
	g.setCapture(ebiten.IsFocused())
	in := &g.b.Ctl

	// Spinner: captured mouse X, gamepad stick / D-pad, arrow keys.
	spin := 0.0
	mx, _ := ebiten.CursorPosition()
	if g.captured {
		spin += float64(mx-g.lastMX) * 0.5
	}
	g.lastMX = mx
	if ps.stickActive {
		spin += ps.sx * 8
	}
	if ebiten.IsKeyPressed(ebiten.KeyLeft) || ebiten.IsKeyPressed(ebiten.KeyA) {
		spin -= 6
	}
	if ebiten.IsKeyPressed(ebiten.KeyRight) || ebiten.IsKeyPressed(ebiten.KeyD) {
		spin += 6
	}
	spin *= float64(g.set.SpinSpeed) / 100
	if g.set.InvertY {
		spin = -spin
	}
	g.spinFrac += spin
	whole := math.Trunc(g.spinFrac)
	g.spinFrac -= whole
	in.Spin = int(clamp(whole, -127, 127))

	mb := func(b ebiten.MouseButton) bool { return g.captured && ebiten.IsMouseButtonPressed(b) }
	k := ebiten.IsKeyPressed
	in.Fire = k(ebiten.KeyZ) || k(ebiten.KeySpace) || k(ebiten.KeyControlLeft) || mb(ebiten.MouseButtonLeft) || ps.fire1
	in.Photon = k(ebiten.KeyX) || k(ebiten.KeyAltLeft) || mb(ebiten.MouseButtonRight) || ps.fire2
	in.Impulse = k(ebiten.KeyC) || k(ebiten.KeyUp) || k(ebiten.KeyW) || ps.fire3
	in.Warp = k(ebiten.KeyV) || k(ebiten.KeyShiftLeft) || mb(ebiten.MouseButtonMiddle) || ps.fire4
	in.Start1 = k(ebiten.Key1) || ps.start
	in.Start2 = k(ebiten.Key2)
	if inpututil.IsKeyJustPressed(ebiten.Key5) || inpututil.IsKeyJustPressed(ebiten.Key6) || ps.coin {
		g.coinHold = 6
	}
	in.Coin1 = g.coinHold > 0
	if g.coinHold > 0 {
		g.coinHold--
	}
	in.Test = k(ebiten.KeyF1)
	in.Service = k(ebiten.Key9)

	g.b.RunCycles(board.CPUHz / tps)
	g.samples = g.b.Snd.TakeSamples(g.samples[:0])
	g.audio.push(g.samples)
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	if g.err != "" {
		ebitenutil.DebugPrintAt(screen, g.err, 20, 20)
		return
	}
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	x, y, gw, gh := gameRect(w, h)
	fgw, fgh := float32(gw), float32(gh)
	newFrame := g.listSeq != g.drawnSeq
	if newFrame {
		g.drawnSeq = g.listSeq
		g.strokes = g.strokes[:0]
		for _, s := range g.list {
			g.strokes = append(g.strokes, gpu.Stroke{
				X0: s.X0 * fgw, Y0: s.Y0 * fgh, X1: s.X1 * fgw, Y1: s.Y1 * fgh, R: s.R, G: s.G, B: s.B,
			})
		}
	}
	overlay := g.menu.open || g.toastTime > 0
	if !overlay {
		g.gpu.Draw(screen, float64(x), float64(y), gw, gh, g.strokes, newFrame)
	} else {
		g.frame = g.frame[:0]
		dim := float32(1)
		if g.menu.open {
			dim = 0.15
		}
		for _, s := range g.strokes {
			s.R, s.G, s.B = s.R*dim, s.G*dim, s.B*dim
			g.frame = append(g.frame, s)
		}
		if g.menu.open {
			g.frame = g.menuStrokes(g.frame, fgw, fgh)
		} else {
			th := fgh / 40
			g.frame = vecText(g.frame, g.toast, (fgw-textWidth(g.toast, th))/2, fgh*0.12, th, 1, 0.95, 0.1)
		}
		p := g.gpu.P
		g.gpu.P.Persistence = 0
		g.gpu.Draw(screen, float64(x), float64(y), gw, gh, g.frame, true)
		g.gpu.P = p
	}
	if g.showFPS {
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("FPS %.0f  TPS %.0f  strokes %d", ebiten.ActualFPS(), ebiten.ActualTPS(), len(g.strokes)), x+10, y+gh-20)
	}
}

func (g *game) Layout(ow, oh int) (int, int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(ow) * s), int(float64(oh) * s)
}

// Window size, overridable for testing with STARTREK_WIN=WxH.
func winSize() (int, int) {
	var w, h int
	if _, err := fmt.Sscanf(os.Getenv("STARTREK_WIN"), "%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 1280, 960
}
