package cli

import (
	"io"

	"github.com/atqamz/hand/skills"
)

func init() {
	commands["skill"] = cmdSkill
}

func cmdSkill(r *runner, args []string) error {
	if _, err := parse(flags("skill"), args, 0); err != nil {
		return err
	}
	_, err := io.WriteString(r.env.Stdout, skills.Hand)
	return err
}
