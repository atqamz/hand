package main

import (
	"os"

	"github.com/atqamz/hand/internal/faketool"
)

func main() {
	os.Exit(faketool.Run())
}
