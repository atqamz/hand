package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

func extractBinary(archivePath, expectedName, destPath string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"):
		return extractTarGz(archivePath, expectedName, destPath)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, expectedName, destPath)
	default:
		return fmt.Errorf("unsupported archive format: %s", archivePath)
	}
}

func extractTarGz(archivePath, expectedName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream in %s: %w", archivePath, err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			if !found {
				return fmt.Errorf("%s not found in %s", expectedName, archivePath)
			}
			return os.Chmod(destPath, 0o755)
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", archivePath, err)
		}
		if !tarEntryMatches(hdr, expectedName) {
			continue
		}
		if found {
			return fmt.Errorf("multiple %s entries in %s", expectedName, archivePath)
		}
		if err := copyArchiveMember(destPath, tr); err != nil {
			return err
		}
		found = true
	}
}

func extractZip(archivePath, expectedName, destPath string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip %s: %w", archivePath, err)
	}
	defer func() { _ = archive.Close() }()

	found := false
	for _, entry := range archive.File {
		if !zipEntryMatches(entry, expectedName) {
			continue
		}
		if found {
			return fmt.Errorf("multiple %s entries in %s", expectedName, archivePath)
		}
		reader, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open %s in %s: %w", entry.Name, archivePath, err)
		}
		err = copyArchiveMember(destPath, reader)
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("close %s in %s: %w", entry.Name, archivePath, closeErr)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("%s not found in %s", expectedName, archivePath)
	}
	return os.Chmod(destPath, 0o755)
}

func tarEntryMatches(entry *tar.Header, expectedName string) bool {
	return (entry.Typeflag == tar.TypeReg || entry.Typeflag == 0) && archiveBase(entry.Name) == expectedName
}

func zipEntryMatches(entry *zip.File, expectedName string) bool {
	return entry.FileInfo().Mode().IsRegular() && archiveBase(entry.Name) == expectedName
}

func archiveBase(name string) string {
	name = strings.TrimSuffix(name, "/")
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func copyArchiveMember(destPath string, reader io.Reader) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	if _, err := io.Copy(out, reader); err != nil {
		_ = out.Close()
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}
	return nil
}
