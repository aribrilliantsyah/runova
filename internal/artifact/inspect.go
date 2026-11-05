package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const unknownFileProblem = "Jenis file tidak dikenali. Unggah .jar, binary Linux, atau .tar.gz"

// Inspect is the single entry point for an uploaded file. A .tar.gz is
// extracted into workDir (total extracted size capped by limit); its single
// artifact is what gets reported. FileName, Size and SHA256 describe that
// artifact: for a .tar.gz the entry inside, otherwise the file itself under
// the name fileName. The returned path is the artifact to keep: path itself,
// or the extracted file in workDir. It is empty when an archive was rejected;
// workDir is then left empty. Env vars merge with
// priority .env.example > properties > scan. The error is only for I/O failures.
func Inspect(path, fileName, workDir string, limit int64) (Report, string, error) {
	format, err := Detect(path)
	if err != nil {
		return Report{}, "", err
	}
	if format == "targz" {
		return inspectTarGz(path, workDir, limit)
	}
	rep, err := inspectFile(path, format, nil)
	if err != nil {
		return Report{}, "", err
	}
	if err := describe(&rep, path, fileName); err != nil {
		return Report{}, "", err
	}
	return rep, path, nil
}

func inspectTarGz(path, workDir string, limit int64) (Report, string, error) {
	artifactPath, envPath, err := ExtractTarGz(path, workDir, limit)
	switch {
	case errors.Is(err, ErrUnsafeArchive), errors.Is(err, ErrTooLarge),
		errors.Is(err, ErrArchiveContents), errors.Is(err, ErrCorruptArchive):
		return problemReport(err.Error()), "", nil
	case err != nil:
		return Report{}, "", err
	}

	var envExample []EnvVar
	if envPath != "" {
		f, err := os.Open(envPath)
		if err != nil {
			return Report{}, "", err
		}
		envExample, err = ParseEnvExample(f)
		f.Close()
		if err != nil {
			return Report{}, "", err
		}
	}

	format, err := Detect(artifactPath)
	if err != nil {
		return Report{}, "", err
	}
	rep, err := inspectFile(artifactPath, format, envExample)
	if err != nil {
		return Report{}, "", err
	}
	if err := describe(&rep, artifactPath, filepath.Base(artifactPath)); err != nil {
		return Report{}, "", err
	}
	return rep, artifactPath, nil
}

// inspectFile runs the inspector for format and merges envExample into the result.
func inspectFile(path, format string, envExample []EnvVar) (Report, error) {
	var (
		rep Report
		err error
	)
	switch format {
	case "jar":
		rep, err = InspectJar(path)
	case "elf", "pe", "macho":
		rep, err = InspectBinary(path)
	default:
		return problemReport(unknownFileProblem), nil
	}
	if err != nil {
		return Report{}, err
	}

	set := envSet{}
	for _, v := range envExample {
		set.add(v)
	}
	for _, v := range rep.Env {
		set.add(v)
	}
	rep.Env = set.list()

	var found folderSet
	for _, v := range envExample {
		found.add(v.Name, v.Default, SourceEnvExample)
	}
	for _, d := range rep.Folders {
		found.addFolder(d)
	}
	rep.Folders = found.list
	if rep.Folders == nil {
		rep.Folders = []DetectedFolder{}
	}

	if rep.Kind == KindStatic || rep.Kind == KindGlibc {
		if port, ok := envPort(envExample); ok {
			rep.Port, rep.PortSource = port, SourceEnvExample
		}
	}
	return rep, nil
}

func envPort(env []EnvVar) (int, bool) {
	for _, v := range env {
		if v.Name != "PORT" {
			continue
		}
		n, err := strconv.Atoi(v.Default)
		if err != nil || n < 1 || n > 65535 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// describe fills the identity fields (name, size, SHA-256) of rep from the file at path.
func describe(rep *Report, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	rep.FileName, rep.Size, rep.SHA256 = filepath.Base(name), n, hex.EncodeToString(h.Sum(nil))
	return nil
}
