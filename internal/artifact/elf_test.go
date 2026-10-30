package artifact

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// buildELF writes a minimal little-endian ELF (header, one PT_LOAD, optional
// PT_INTERP) and appends each extra string NUL-terminated.
func buildELF(t *testing.T, machine elf.Machine, class elf.Class, interp string, extra ...string) string {
	t.Helper()
	var (
		ehsize, phsize = 64, 56
		phnum          = 1
	)
	if class == elf.ELFCLASS32 {
		ehsize, phsize = 52, 32
	}
	if interp != "" {
		phnum++
	}
	interpOff := ehsize + phnum*phsize
	dataOff := interpOff
	if interp != "" {
		dataOff += len(interp) + 1
	}

	var buf bytes.Buffer
	le := binary.LittleEndian
	ident := [16]byte{0x7f, 'E', 'L', 'F', byte(class), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)}
	buf.Write(ident[:])
	w := func(v any) {
		if err := binary.Write(&buf, le, v); err != nil {
			t.Fatal(err)
		}
	}
	w(uint16(elf.ET_EXEC))
	w(uint16(machine))
	w(uint32(1))
	if class == elf.ELFCLASS32 {
		w(uint32(0x400000))
		w(uint32(ehsize))
		w(uint32(0))
		w(uint32(0))
		w(uint16(ehsize))
		w(uint16(phsize))
		w(uint16(phnum))
		w(uint16(0))
		w(uint16(0))
		w(uint16(0))
	} else {
		w(uint64(0x400000))
		w(uint64(ehsize))
		w(uint64(0))
		w(uint32(0))
		w(uint16(ehsize))
		w(uint16(phsize))
		w(uint16(phnum))
		w(uint16(0))
		w(uint16(0))
		w(uint16(0))
	}
	phdr := func(typ elf.ProgType, off, size int) {
		if class == elf.ELFCLASS32 {
			w(uint32(typ))
			w(uint32(off))
			w(uint32(0x400000 + off))
			w(uint32(0x400000 + off))
			w(uint32(size))
			w(uint32(size))
			w(uint32(elf.PF_R))
			w(uint32(1))
			return
		}
		w(uint32(typ))
		w(uint32(elf.PF_R))
		w(uint64(off))
		w(uint64(0x400000 + off))
		w(uint64(0x400000 + off))
		w(uint64(size))
		w(uint64(size))
		w(uint64(1))
	}
	if interp != "" {
		phdr(elf.PT_INTERP, interpOff, len(interp)+1)
	}
	phdr(elf.PT_LOAD, 0, dataOff)
	if interp != "" {
		buf.WriteString(interp)
		buf.WriteByte(0)
	}
	for _, s := range extra {
		buf.WriteString(s)
		buf.WriteByte(0)
	}
	path := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(path, buf.Bytes(), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"jar", []byte("PK\x03\x04rest"), "jar"},
		{"elf", []byte("\x7fELF\x02\x01"), "elf"},
		{"targz", []byte{0x1f, 0x8b, 8, 0}, "targz"},
		{"pe", []byte("MZ\x90\x00"), "pe"},
		{"macho32", []byte{0xfe, 0xed, 0xfa, 0xce, 0}, "macho"},
		{"macho64", []byte{0xfe, 0xed, 0xfa, 0xcf, 0}, "macho"},
		{"macho64le", []byte{0xcf, 0xfa, 0xed, 0xfe, 0}, "macho"},
		{"fat", []byte{0xca, 0xfe, 0xba, 0xbe, 0}, "macho"},
		{"text", []byte("hello"), "unknown"},
		{"short", []byte("P"), "unknown"},
		{"empty", nil, "unknown"},
	}
	for _, c := range cases {
		got, err := Detect(writeFile(t, c.name, c.data))
		if err != nil || got != c.want {
			t.Errorf("%s: Detect = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := Detect(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("want error for missing file")
	}
}

func TestELFStaticAndGlibc(t *testing.T) {
	rep, err := InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != KindStatic || !rep.OK() || rep.Port != 8080 || rep.PortSource != "bawaan" {
		t.Errorf("static: %+v", rep)
	}

	rep, err = InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "/lib64/ld-linux-x86-64.so.2"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != KindGlibc || !rep.OK() || rep.Port != 8080 {
		t.Errorf("glibc: %+v", rep)
	}
}

func TestELFRejections(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"musl", buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "/lib/ld-musl-x86_64.so.1"), "Binary musl dinamis tidak didukung; build statis"},
		{"other interp", buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "/opt/weird/ld.so"), "/opt/weird/ld.so"},
		{"arm64", buildELF(t, elf.EM_AARCH64, elf.ELFCLASS64, ""), "Binary ARM64, server ini amd64"},
		{"riscv", buildELF(t, elf.EM_RISCV, elf.ELFCLASS64, ""), "server ini amd64"},
		{"32-bit", buildELF(t, elf.EM_386, elf.ELFCLASS32, ""), "Binary 32-bit tidak didukung"},
		{"pe", writeFile(t, "a.exe", []byte("MZ\x90\x00\x03")), "File Windows (.exe) tidak bisa dijalankan di Linux"},
		{"macho", writeFile(t, "a.macho", []byte{0xcf, 0xfa, 0xed, 0xfe, 7, 0, 0, 1}), "Binary macOS tidak bisa dijalankan di Linux"},
	}
	for _, c := range cases {
		rep, err := InspectBinary(c.path)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if rep.OK() || !hasProblem(rep, c.want) {
			t.Errorf("%s: problems = %v, want one containing %q", c.name, rep.Problems, c.want)
		}
	}
}

func TestELFTruncatedIsProblem(t *testing.T) {
	rep, err := InspectBinary(writeFile(t, "bad", []byte("\x7fELF\x02\x01\x01")))
	if err != nil {
		t.Fatal(err)
	}
	if !hasProblem(rep, "ELF tidak valid") {
		t.Errorf("problems = %v", rep.Problems)
	}
}

func TestScanEnv(t *testing.T) {
	path := buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "",
		"os.Getenv", "DATABASE_URL", "JWT_SECRET", "GOMAXPROCS", "LC_ALL", "lowercase_x", "PORT",
		"HTTP_PROXY", "RUST_BACKTRACE", "CGO_ENABLED", "XDG_DATA", "AB_", "X_Y", "GODEBUG_X", "GO_THING")
	rep, err := InspectBinary(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := envNames(rep.Env); !slices.Equal(got, []string{"DATABASE_URL", "JWT_SECRET", "PORT"}) {
		t.Fatalf("env = %v", got)
	}
	for _, e := range rep.Env {
		if e.Source != SourceScan || e.Secret != (e.Name == "JWT_SECRET") {
			t.Errorf("%+v", e)
		}
	}
}

func TestScanEnvAcceptsOtherMarkers(t *testing.T) {
	for _, marker := range []string{"syscall.Getenv", "std::env::var", "getenv"} {
		rep, err := InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "", marker, "DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		if got := envNames(rep.Env); !slices.Equal(got, []string{"DATABASE_URL"}) {
			t.Errorf("%s: env = %v", marker, got)
		}
	}
}

func TestScanEnvNeedsMarker(t *testing.T) {
	rep, err := InspectBinary(buildELF(t, elf.EM_X86_64, elf.ELFCLASS64, "", "DATABASE_URL", "JWT_SECRET", "PORT"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Env) != 0 {
		t.Errorf("env = %v", rep.Env)
	}
}

func TestScanEnvAcrossChunkBoundary(t *testing.T) {
	for _, offset := range []int{1, 5, 11, 12, 13, 63} {
		var data bytes.Buffer
		data.Write(bytes.Repeat([]byte{0}, scanChunk-offset))
		data.WriteString("DATABASE_URL")
		data.WriteByte(0)
		data.Write(bytes.Repeat([]byte{0}, scanChunk))
		data.WriteString("get")
		data.WriteString("env")
		env, err := scanEnv(bytes.NewReader(data.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if got := envNames(env); !slices.Equal(got, []string{"DATABASE_URL"}) {
			t.Errorf("offset %d: env = %v", offset, got)
		}
	}
}

func TestScanEnvMarkerAcrossChunkBoundary(t *testing.T) {
	var data bytes.Buffer
	data.WriteString("DATABASE_URL\x00")
	data.Write(bytes.Repeat([]byte{0}, scanChunk-len("DATABASE_URL\x00")-3))
	data.WriteString("os.Getenv")
	env, err := scanEnv(bytes.NewReader(data.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 {
		t.Errorf("env = %v", env)
	}
}

func TestScanEnvOverlongRunIsNotAName(t *testing.T) {
	long := strings.Repeat("A", 40) + "_" + strings.Repeat("B", 40)
	env, err := scanEnv(strings.NewReader("getenv\x00" + long + "\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Errorf("env = %v", env)
	}
}

func TestScanEnvCapsAtFifty(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("getenv\x00")
	for i := range 80 {
		sb.WriteString("APP_KEY_")
		sb.WriteString(string(rune('A' + i/26)))
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteByte(0)
	}
	env, err := scanEnv(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 50 {
		t.Errorf("len = %d", len(env))
	}
}

func TestScanEnvOverCapKeepsOnlyLikelyNames(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("getenv\x00DATABASE_URL\x00")
	for i := range 80 {
		sb.WriteString("HTTP2_FRAME_")
		sb.WriteString(string(rune('A' + i/26)))
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteByte(0)
	}
	env, err := scanEnv(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got := envNames(env); !slices.Equal(got, []string{"DATABASE_URL"}) {
		t.Errorf("env = %v", got)
	}
}
