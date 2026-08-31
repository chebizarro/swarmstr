package nip67

import "testing"

func TestParseEOSECompleteness(t *testing.T) {
	tests := []struct {
		payload string
		want    Completeness
	}{
		{`["EOSE","legacy"]`, Unknown},
		{`["EOSE","done",["finish","future"]]`, Complete},
		{`["EOSE","page",["more"]]`, More},
		{`["EOSE","safe",["finish","more"]]`, More},
	}
	for _, tt := range tests {
		eose, err := ParseEOSE([]byte(tt.payload))
		if err != nil {
			t.Fatalf("ParseEOSE(%s): %v", tt.payload, err)
		}
		if got := eose.CompletenessHint(); got != tt.want {
			t.Fatalf("ParseEOSE(%s) completeness=%v want %v", tt.payload, got, tt.want)
		}
	}
}

func TestParseEOSERejectsMalformedHints(t *testing.T) {
	if _, err := ParseEOSE([]byte(`["EOSE","sub","finish"]`)); err == nil {
		t.Fatal("expected malformed hint array error")
	}
}
