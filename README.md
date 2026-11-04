# Star Trek (Sega, 1982) - native port

Sega's 1982 *Star Trek: Strategic Operations Simulator* (G80 vector hardware) as a native Go + Ebitengine
program, same pipeline as Star Wars: the board is ported to Go and your original ROMs run unmodified on top
(loaded at runtime, never included). Video uses the Star Wars vector renderer - no pixel framebuffer.

## Running

Put `startrek.zip` (MAME `startrek` set) next to the program. Files are matched by CRC.

## Controls

| Input | Action |
|---|---|
| Mouse left / right | Spinner (mouse is captured while playing; Esc / menu releases it) |
| Arrows / A, D | Spinner |
| Left click / Z / Space | Fire (phasers) |
| Right click / X | Photon torpedoes |
| Up / W / C | Impulse |
| Middle click / V / Shift | Warp |
| 1 / 2 | Start |
| 5 | Coin |
| R | Reverse spinner |
| Esc / Tab | Settings menu (pauses, releases mouse) |
| F2 / F3 / F11 | Look preset / FPS / full screen |

**Gamepad:** left stick or D-pad = spinner, A / RT = fire, B / LT = photon, X = impulse, Y = warp,
Start = start, Select = coin, right bumper = menu, hold Select + Start to quit.

## Status

- Z80 main board (315-0064 security chip, wait states, multiplier, spinner, interrupts) and Sega vector
  generator; matches MAME frame for frame.
- Sound: Universal Sound Board (8035 running the uploaded program, 3 x 8253, noise, RC filters) and
  Speech Board (8035 + SP0250), ported from MAME and checked against MAME's output by level and spectrogram.
  Menu: Volume, Speech, Effects. `STARTREK_NOAUDIO=1` runs without an audio device.

## Tools

```
go run ./cmd/shot roms.zip out [-coin 5 -start 7 -spin 6] 10 15   # headless vector PNGs
go run ./cmd/wav  roms.zip out.wav 30 5 7                        # headless audio capture
```

## Releases

Publishing a GitHub release runs `.github/workflows/release.yml`: `startrek.exe`, `StarTrek-linux.tar.gz`
(Steam Deck / SteamOS), `StarTrek-mac.zip`.
