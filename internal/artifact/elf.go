package artifact

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

const glibcInterp = "/lib64/ld-linux-x86-64.so.2"

// Detect classifies a file by its magic bytes: "jar", "elf", "targz", "pe",
// "macho" or "unknown".
func Detect(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var magic [4]byte
	n, err := io.ReadFull(f, magic[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	m := magic[:n]
	switch {
	case bytes.HasPrefix(m, []byte("PK\x03\x04")):
		return "jar", nil
	case bytes.HasPrefix(m, []byte("\x7fELF")):
		return "elf", nil
	case bytes.HasPrefix(m, []byte{0x1f, 0x8b}):
		return "targz", nil
	case bytes.HasPrefix(m, []byte("MZ")):
		return "pe", nil
	case bytes.Equal(m, []byte{0xfe, 0xed, 0xfa, 0xce}),
		bytes.Equal(m, []byte{0xfe, 0xed, 0xfa, 0xcf}),
		bytes.Equal(m, []byte{0xcf, 0xfa, 0xed, 0xfe}),
		bytes.Equal(m, []byte{0xca, 0xfe, 0xba, 0xbe}):
		return "macho", nil
	}
	return "unknown", nil
}

// InspectBinary reports on an ELF binary. Windows and macOS executables are
// reported as problems with a message naming their kind. The error is only
// for I/O failures.
func InspectBinary(path string) (Report, error) {
	format, err := Detect(path)
	if err != nil {
		return Report{}, err
	}
	switch format {
	case "pe":
		return problemReport("File Windows (.exe) tidak bisa dijalankan di Linux"), nil
	case "macho":
		return problemReport("Binary macOS tidak bisa dijalankan di Linux"), nil
	case "elf":
	default:
		return problemReport("Bukan binary ELF"), nil
	}

	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	ef, err := elf.NewFile(f)
	if err != nil {
		return problemReport("File ELF tidak valid"), nil
	}
	defer ef.Close()

	if msg := elfProblem(ef); msg != "" {
		return problemReport(msg), nil
	}
	interp, err := elfInterp(ef)
	if err != nil {
		return problemReport("File ELF tidak valid"), nil
	}
	var rep Report
	switch interp {
	case "":
		rep = newReport(KindStatic)
	case glibcInterp:
		rep = newReport(KindGlibc)
	default:
		if strings.Contains(interp, "musl") {
			return problemReport("Binary musl dinamis tidak didukung; build statis"), nil
		}
		return problemReport(fmt.Sprintf("Binary dinamis dengan interpreter %s tidak didukung; build statis atau dengan glibc", interp)), nil
	}

	if rep.Language, err = binaryLanguage(ef, io.NewSectionReader(f, 0, math.MaxInt64)); err != nil {
		return Report{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Report{}, err
	}
	if rep.Env, err = scanEnv(f); err != nil {
		return Report{}, err
	}
	return rep, nil
}

func elfProblem(ef *elf.File) string {
	switch {
	case ef.Class == elf.ELFCLASS32:
		return "Binary 32-bit tidak didukung"
	case ef.Machine == elf.EM_AARCH64:
		return "Binary ARM64, server ini amd64"
	case ef.Machine != elf.EM_X86_64:
		return fmt.Sprintf("Binary %s, server ini amd64", strings.TrimPrefix(ef.Machine.String(), "EM_"))
	case ef.OSABI != elf.ELFOSABI_NONE && ef.OSABI != elf.ELFOSABI_LINUX:
		return "Binary bukan untuk Linux"
	case ef.Type != elf.ET_EXEC && ef.Type != elf.ET_DYN:
		return "File ELF bukan program yang bisa dijalankan"
	}
	return ""
}

// elfInterp returns the PT_INTERP path, or "" for a binary without one.
func elfInterp(ef *elf.File) (string, error) {
	for _, p := range ef.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		if p.Filesz > 4096 {
			return "", fmt.Errorf("interpreter path too long")
		}
		b := make([]byte, p.Filesz)
		if _, err := io.ReadFull(p.Open(), b); err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\x00"), nil
	}
	return "", nil
}
