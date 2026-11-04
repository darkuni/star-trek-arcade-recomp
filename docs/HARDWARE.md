# Star Trek (Sega G80 vector) hardware notes

Reference: MAME segag80v.cpp, segag80v_v.cpp, segag80_m.cpp, segausb.cpp, segaspeech.cpp.

- Z80 at 3.867 MHz, 2 wait states per memory access (4 on opcode fetch).
- 315-0064 security chip scrambles the low address byte of `LD (nn),A` writes by PC & 3.
- Map: $0000-$bfff ROM, $c800-$cfff RAM, $d000-$dfff USB RAM (8035 program uploaded by the main CPU),
  $e000-$efff vector RAM.
- Ports: $bd/$be multiplier, $f8 spinner select, $f8-$fb mangled DIP/coin/DRAW reads, $fc buttons/spinner,
  $38/$3b speech, $3f USB.
- IRQ (IM 1): 40 Hz vector refresh and coin edges. NMI: test switch.
- Vector generator: symbol list -> vector lists, sine PROM, 6-bit colour, per-frame time budget.

## Sound (next)

1. Intel 8035 core (both boards).
2. Universal Sound Board: 8035 + 3 x 8253 + noise + RC filters.
3. Speech board: 8035 + SP0250 LPC chip.
