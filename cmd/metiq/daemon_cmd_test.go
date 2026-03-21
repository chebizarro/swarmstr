package main

import "testing"

func TestLooksLikeMetiqdCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "renamed absolute path", cmd: "/usr/local/bin/metiqd --pid-file /tmp/x.pid", want: true},
		{name: "renamed bare executable", cmd: "metiqd --config x", want: true},
		{name: "renamed windows exe name", cmd: `C:\\tools\\metiqd.exe --pid-file x`, want: true},
		{name: "different process", cmd: "/usr/bin/python worker.py", want: false},
		{name: "empty", cmd: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeMetiqdCommand(tc.cmd); got != tc.want {
				t.Fatalf("looksLikeMetiqdCommand(%q)=%v want=%v", tc.cmd, got, tc.want)
			}
		})
	}
}
