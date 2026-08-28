// Package pathdisplay renders a filesystem path for a message that names it only as
// context, never as something the operator is told to type or run - that job belongs to
// internal/shellquote instead.
package pathdisplay

// Context delimits path with backticks, hand's existing convention for every other literal
// token shown to the operator. Backticks need no escaping, so a path renders unchanged
// instead of %q doubling its separators on Windows.
func Context(path string) string {
	return "`" + path + "`"
}
