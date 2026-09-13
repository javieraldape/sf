package executionpolicy

import "testing"

func TestPureGoCommandPolicy(t *testing.T) {
	good := []string{"go", "--sf-go-pure-files-v1", "internals/helpers.go", "internals/helpers_test.go"}
	if !EvaluateRepositoryCommand(good).Allowed {
		t.Fatal("exact pure recipe refused")
	}
	for _, argv := range [][]string{
		{"go", "test", "internals/helpers.go", "internals/helpers_test.go"},
		append(append([]string{}, good...), "-exec=sh"),
		{"go", "--sf-go-pure-files-v1", "../helpers.go", "../helpers_test.go"},
		{"go", "--sf-go-pure-files-v1", "helpers.go", "different_test.go"},
	} {
		if EvaluateRepositoryCommand(argv).Allowed {
			t.Fatalf("accepted %q", argv)
		}
	}
}
