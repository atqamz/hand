package luvus_test

import "testing"

func TestSocketPathUsesLuvusTempRoot(t *testing.T) { socketUnder(t, "/tmp") }
