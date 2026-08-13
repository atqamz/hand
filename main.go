package main

import "github.com/atqamz/hand/cmd"

var version = "dev"
var channel = "dev"
var commit = ""

func main() {
	cmd.Execute(version, channel, commit)
}
