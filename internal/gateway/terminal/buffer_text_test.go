package terminal

import "testing"

func TestRenderTextStripsAnsiSequences(t *testing.T) {
	in := "\x1b[1;32mgreen\x1b[0m plain \x1b]0;window title\x07tail"
	if got := RenderText(in); got != "green plain tail" {
		t.Fatalf("unexpected render: %q", got)
	}
}

func TestRenderTextCollapsesCarriageReturnOverwrites(t *testing.T) {
	if got := RenderText("10%\r20%\r30%"); got != "30%" {
		t.Fatalf("progress overwrite not collapsed: %q", got)
	}
	// A trailing \r before \n keeps the written text.
	if got := RenderText("line one\r\nline two"); got != "line one\nline two" {
		t.Fatalf("crlf handling broken: %q", got)
	}
}

func TestRenderTextDropsControlBytesKeepsTab(t *testing.T) {
	in := "a\x00b\tc\x07d"
	if got := RenderText(in); got != "ab\tcd" {
		t.Fatalf("control byte handling broken: %q", got)
	}
}

func TestRenderTextKeepsPlainMultilineOutput(t *testing.T) {
	in := "$ ls\nfile-a\nfile-b\n"
	if got := RenderText(in); got != in {
		t.Fatalf("plain text mangled: %q", got)
	}
}
