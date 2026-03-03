package methods

import (
	"errors"
	"net/http"
	"testing"
)

func TestMapNIP86Error_InvalidParams(t *testing.T) {
	errObj := MapNIP86Error(http.StatusBadRequest, errors.New("invalid params"))
	if errObj.Code != -32602 {
		t.Fatalf("unexpected code: %d", errObj.Code)
	}
	if errObj.Message != "invalid params" {
		t.Fatalf("unexpected message: %q", errObj.Message)
	}
}

func TestMapNIP86Error_PreconditionData(t *testing.T) {
	errObj := MapNIP86Error(http.StatusConflict, &PreconditionConflictError{
		Resource:        "config",
		ExpectedVersion: 1,
		CurrentVersion:  2,
		ExpectedEvent:   "evt-a",
		CurrentEvent:    "evt-b",
	})
	if errObj.Code != -32010 {
		t.Fatalf("unexpected code: %d", errObj.Code)
	}
	if errObj.Data == nil {
		t.Fatal("expected conflict data")
	}
	if got, _ := errObj.Data["current_version"].(int); got != 2 {
		t.Fatalf("unexpected current_version: %#v", errObj.Data["current_version"])
	}
}
