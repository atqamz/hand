package main

import "github.com/atqamz/hand/cmd"

var version = "dev"
var channel = "dev"
var commit = ""
var distribution = ""

func main() {
	cmd.Execute(version, channel, commit, distribution)
}
