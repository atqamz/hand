package herdr

import (
	"fmt"
	"unicode"

	"github.com/atqamz/hand/internal/shellquote"
)

func renderPOSIX(executable string, args []string) (string, error) {
	if err := validateShellValue("executable", executable); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellquote.Quote(executable))
	for i, arg := range args {
		if err := validateShellValue(fmt.Sprintf("argument %d", i), arg); err != nil {
			return "", err
		}
		parts = append(parts, shellquote.Quote(arg))
	}
	return joinShellParts(parts), nil
}

func validateShellValue(name, value string) error {
	if value == "" && name == "executable" {
		return fmt.Errorf("shell %s is empty", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("shell %s contains unsupported control character", name)
		}
	}
	return nil
}

func joinShellParts(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += " "
		}
		result += part
	}
	return result
}
