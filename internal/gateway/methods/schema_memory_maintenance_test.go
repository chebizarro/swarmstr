package methods

import (
	"encoding/json"
	"testing"
)

func TestDoctorMemoryRepairSafeOnlyDefault(t *testing.T) {
	// Omitted safeOnly defaults to true.
	req, err := DecodeDoctorMemoryRepairParams(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !req.SafeOnly {
		t.Fatalf("expected safeOnly to default to true")
	}

	// Explicit false is preserved.
	req, err = DecodeDoctorMemoryRepairParams(json.RawMessage(`{"safeOnly":false,"fixAll":true}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.SafeOnly {
		t.Fatalf("explicit safeOnly=false must be preserved")
	}
	if !req.FixAll {
		t.Fatalf("expected fixAll=true")
	}
}

func TestRemHarnessPhaseNormalization(t *testing.T) {
	cases := map[string]bool{
		`{}`:                true,
		`{"phase":"REM"}`:   true,
		`{"phase":"light"}`: true,
		`{"phase":"deep"}`:  false,
		`{"limit":-1}`:      false,
		`{"apply":true}`:    true,
	}
	for params, wantOK := range cases {
		req, err := DecodeDoctorMemoryRemHarnessParams(json.RawMessage(params))
		if err != nil {
			t.Fatalf("decode %s: %v", params, err)
		}
		_, err = req.Normalize()
		if wantOK && err != nil {
			t.Fatalf("normalize %s: unexpected error %v", params, err)
		}
		if !wantOK && err == nil {
			t.Fatalf("normalize %s: expected error", params)
		}
	}
}

func TestRemHarnessLimitClamp(t *testing.T) {
	req, err := DecodeDoctorMemoryRemHarnessParams(json.RawMessage(`{"limit":999999}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.Limit != maxRemHarnessLimit {
		t.Fatalf("expected limit clamped to %d, got %d", maxRemHarnessLimit, req.Limit)
	}
}
