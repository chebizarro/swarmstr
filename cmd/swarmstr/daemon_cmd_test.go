package main

import "testing"

func TestLooksLikeSwarmstrdCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "absolute path", cmd: "/usr/local/bin/swarmstrd --pid-file /tmp/x.pid", want: true},
		{name: "bare executable", cmd: "swarmstrd --config x", want: true},
		{name: "windows exe name", cmd: `C:\\tools\\swarmstrd.exe --pid-file x`, want: true},
		{name: "different process", cmd: "/usr/bin/python worker.py", want: false},
		{name: "empty", cmd: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeSwarmstrdCommand(tc.cmd); got != tc.want {
				t.Fatalf("looksLikeSwarmstrdCommand(%q)=%v want=%v", tc.cmd, got, tc.want)
			}
		})
	}
}
