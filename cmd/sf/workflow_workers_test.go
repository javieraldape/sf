package main

import "testing"

func TestWorkflowWorkerCountIsBoundedAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 2}, {"1", 1}, {"2", 2}, {"3", 3},
		{"0", 0}, {"4", 0}, {"-1", 0}, {"03", 0}, {" 3", 0}, {"3 ", 0}, {"unlimited", 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := workflowWorkerCount(tc.value)
			if got != tc.want || (err != nil) != (tc.want == 0) {
				t.Fatalf("workers=%d err=%v; want %d", got, err, tc.want)
			}
		})
	}
}
