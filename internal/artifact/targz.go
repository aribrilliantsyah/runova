package artifact

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrUnsafeArchive   = errors.New("Arsip berisi path tidak aman")
	ErrTooLarge        = errors.New("Isi arsip melebihi batas ukuran")
	ErrArchiveContents = errors.New("Arsip harus berisi satu file .jar atau binary di akarnya")
	ErrCorruptArchive  = errors.New("Arsip tidak bisa dibaca")
)

const (
	envExampleName = ".env.example"
	maxEnvExample  = 1 << 20
	maxEntryName   = 255
	// tarOverhead is the decompressed stream allowance beyond the content
	// limit for tar headers and padding.
	tarOverhead = 1 << 20
)

// capReader fails with ErrTooLarge once more than max bytes have been read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left < 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return max(0, n-1), ErrTooLarge
	}
	return n, err
}

// ExtractTarGz unpacks a .tar.gz holding exactly one artifact in its root,
// plus an optional .env.example and README*/LICENSE* files, into dir. Total
// extracted content, and the decompressed stream, are capped by limit. On any
// error everything written to dir is removed. envExample is "" when absent.
func ExtractTarGz(src, dir string, limit int64) (artifactPath, envExample string, err error) {
	var written []string
	defer func() {
		if err != nil {
			for _, p := range written {
				os.Remove(p)
			}
			artifactPath, envExample = "", ""
		}
	}()

	f, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", "", ErrCorruptArchive
	}
	defer gz.Close()
	tr := tar.NewReader(&capReader{r: gz, left: limit + tarOverhead})

	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", readErr(err)
		}
		switch hdr.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeReg, tar.TypeDir:
		default:
			return "", "", ErrUnsafeArchive
		}
		name, ok := cleanEntryName(hdr.Name)
		if hdr.Typeflag == tar.TypeDir {
			if !ok && !isRootDir(hdr.Name) {
				return "", "", ErrUnsafeArchive
			}
			continue
		}
		if !ok {
			return "", "", ErrUnsafeArchive
		}
		if strings.Contains(name, "/") {
			return "", "", ErrArchiveContents
		}
		if hdr.Size > limit-total {
			return "", "", ErrTooLarge
		}

		isEnv := name == envExampleName
		if isEnv && hdr.Size > maxEnvExample {
			return "", "", ErrArchiveContents
		}
		if !isEnv && ignoredEntry(name) {
			n, err := io.Copy(io.Discard, io.LimitReader(tr, hdr.Size))
			if err != nil {
				return "", "", readErr(err)
			}
			total += n
			continue
		}
		if (isEnv && envExample != "") || (!isEnv && artifactPath != "") {
			return "", "", ErrArchiveContents
		}

		dst := filepath.Join(dir, name)
		written = append(written, dst)
		n, err := writeEntry(dst, tr, limit-total)
		if err != nil {
			return "", "", err
		}
		total += n
		if isEnv {
			envExample = dst
		} else {
			artifactPath = dst
		}
	}
	if artifactPath == "" {
		return "", "", ErrArchiveContents
	}
	return artifactPath, envExample, nil
}

func readErr(err error) error {
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	return ErrCorruptArchive
}

// writeEntry copies at most remaining bytes of r to a new file at dst.
func writeEntry(dst string, r io.Reader, remaining int64) (int64, error) {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, ErrArchiveContents
		}
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(r, remaining+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case n > remaining:
		return n, ErrTooLarge
	case err != nil:
		return n, readErr(err)
	}
	return n, nil
}

// cleanEntryName normalises a tar entry name. It reports false for absolute
// paths and any path with a ".." element. The archive root ("./") yields ".".
func cleanEntryName(name string) (string, bool) {
	// Over-long names and control characters (NUL included) cannot be created
	// on disk; refusing them here keeps a crafted archive from surfacing as an
	// I/O error (HTTP 500) instead of a rejected upload.
	if name == "" || strings.HasPrefix(name, "/") || len(name) > maxEntryName ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", false
	}
	for elem := range strings.SplitSeq(name, "/") {
		if elem == ".." {
			return "", false
		}
	}
	name = path.Clean(name)
	return name, name != "."
}

func isRootDir(name string) bool { return path.Clean(name) == "." && !strings.HasPrefix(name, "/") }

func ignoredEntry(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, "README") || strings.HasPrefix(upper, "LICENSE")
}
