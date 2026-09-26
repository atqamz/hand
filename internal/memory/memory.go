package memory

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const OperatorFile = "operator.md"

const operatorTemplate = "# Operator memory\nWrite durable preferences and constraints for the supervisor here.\n"

func Init(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "memory", "projects"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(home, "memory", OperatorFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(operatorTemplate); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
