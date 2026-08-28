package toolchain

import (
	"reflect"
	"testing"
)

func TestGitArgsWithTemplatePrependsInitTemplateDir(t *testing.T) {
	got := GitArgsWithTemplate("/fake/git-templates", []string{"clone", "--no-local", "src", "dst"})
	want := []string{"-c", "init.templateDir=/fake/git-templates", "clone", "--no-local", "src", "dst"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GitArgsWithTemplate() = %v, want %v", got, want)
	}
}

func TestGitArgsWithTemplateLeavesArgsUnchangedWhenNoDirectory(t *testing.T) {
	args := []string{"init", "--initial-branch=main", "path"}
	got := GitArgsWithTemplate("", args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("GitArgsWithTemplate() = %v, want %v unchanged", got, args)
	}
}
