// Package assets loads the Sega Star Trek (1982) ROM set at run time.
// The ROMs are not part of this repository; supply your own dump.
//
// Files are matched by CRC32, not by name, so MAME names or anything else work.
package assets

import (
	"archive/zip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ROMs holds every image the boards need, placed in their address spaces.
type ROMs struct {
	Main       [0xc000]byte // main Z80 $0000-$bfff
	SpeechCPU  [0x800]byte  // speech board 8035 program
	SpeechPROM [0x20]byte   // speech board addressing PROM
	SpeechData [0x4000]byte // SP0250 speech data
	PROMs      [0x420]byte  // $000: vector sine table (s-c.xyt-u39), $400: CPU board addressing
}

type part struct {
	mame string
	crc  uint32
	size int
	put  func(r *ROMs, b []byte)
}

// MAME "startrek".
var layout = []part{
	{"1873.cpu-u25", 0xbe46f5d9, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x0:], b) }},
	{"1848.prom-u1", 0x65e3baf3, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x800:], b) }},
	{"1849.prom-u2", 0x8169fd3d, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x1000:], b) }},
	{"1850.prom-u3", 0x78fd68dc, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x1800:], b) }},
	{"1851.prom-u4", 0x3f55ab86, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x2000:], b) }},
	{"1852.prom-u5", 0x2542ecfb, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x2800:], b) }},
	{"1853.prom-u6", 0x75c2526a, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x3000:], b) }},
	{"1854.prom-u7", 0x096d75d0, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x3800:], b) }},
	{"1855.prom-u8", 0xbc7b9a12, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x4000:], b) }},
	{"1856.prom-u9", 0xed9fe2fb, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x4800:], b) }},
	{"1857.prom-u10", 0x28699d45, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x5000:], b) }},
	{"1858.prom-u11", 0x3a7593cb, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x5800:], b) }},
	{"1859.prom-u12", 0x5b11886b, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x6000:], b) }},
	{"1860.prom-u13", 0x62eb96e6, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x6800:], b) }},
	{"1861.prom-u14", 0x99852d1d, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x7000:], b) }},
	{"1862.prom-u15", 0x76ce27b2, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x7800:], b) }},
	{"1863.prom-u16", 0xdd92d187, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x8000:], b) }},
	{"1864.prom-u17", 0xe37d3a1e, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x8800:], b) }},
	{"1865.prom-u18", 0xb2ec8125, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x9000:], b) }},
	{"1866.prom-u19", 0x6f188354, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0x9800:], b) }},
	{"1867.prom-u20", 0xb0a3eae8, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0xa000:], b) }},
	{"1868.prom-u21", 0x8b4e2e07, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0xa800:], b) }},
	{"1869.prom-u22", 0xe5663070, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0xb000:], b) }},
	{"1870.prom-u23", 0x4340616d, 0x800, func(r *ROMs, b []byte) { copy(r.Main[0xb800:], b) }},
	{"1607.speech-u7", 0xb779884b, 0x800, func(r *ROMs, b []byte) { copy(r.SpeechCPU[0x0:], b) }},
	{"6331.speech-u30", 0xadcb81d0, 0x20, func(r *ROMs, b []byte) { copy(r.SpeechPROM[0x0:], b) }},
	{"1871.speech-u6", 0x03713920, 0x1000, func(r *ROMs, b []byte) { copy(r.SpeechData[0x0:], b) }},
	{"1872.speech-u5", 0xebb5c3a9, 0x1000, func(r *ROMs, b []byte) { copy(r.SpeechData[0x1000:], b) }},
	{"s-c.xyt-u39", 0x56484d19, 0x400, func(r *ROMs, b []byte) { copy(r.PROMs[0x0:], b) }},
	{"pr-82.cpu-u15", 0xc609b79e, 0x20, func(r *ROMs, b []byte) { copy(r.PROMs[0x400:], b) }},
}

// MissingError lists the ROM images that were not found.
type MissingError struct {
	Missing []string
	Sources []string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("missing ROM files: %s", strings.Join(e.Missing, ", "))
}

func collect(path string, blobs *[][]byte) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		ents, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range ents {
			p := filepath.Join(path, e.Name())
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
				_ = collect(p, blobs)
				continue
			}
			if b, err := os.ReadFile(p); err == nil && len(b) <= 0x10000 {
				*blobs = append(*blobs, b)
			}
		}
		return nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 > 0x10000 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err == nil {
			*blobs = append(*blobs, b)
		}
	}
	return nil
}

// Load reads the ROM set from the given zips and folders.
func Load(paths ...string) (*ROMs, error) {
	var blobs [][]byte
	var srcs []string
	for _, p := range paths {
		if err := collect(p, &blobs); err == nil {
			srcs = append(srcs, p)
		}
	}
	byCRC := map[uint32][]byte{}
	for _, b := range blobs {
		byCRC[crc32.ChecksumIEEE(b)] = b
	}
	r := &ROMs{}
	var missing []string
	for _, pt := range layout {
		b, ok := byCRC[pt.crc]
		if !ok || len(b) != pt.size {
			missing = append(missing, pt.mame)
			continue
		}
		pt.put(r, b)
	}
	if len(missing) > 0 {
		return nil, &MissingError{Missing: missing, Sources: srcs}
	}
	return r, nil
}
