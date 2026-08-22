package herdr

import (
	"fmt"
	"strings"
)

func renderPowerShell(executable string, args []string) (string, error) {
	if err := validateShellValue("executable", executable); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, "&", powerShellQuote(executable))
	for i, arg := range args {
		if err := validateShellValue(fmt.Sprintf("argument %d", i), arg); err != nil {
			return "", err
		}
		parts = append(parts, powerShellQuote(arg))
	}
	return joinShellParts(parts), nil
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
