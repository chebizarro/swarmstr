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

func TestMapNIP86Error_AuthAndMethodMappings(t *testing.T) {
	unauth := MapNIP86Error(http.StatusUnauthorized, errors.New("authentication required"))
	if unauth.Code != -32001 {
		t.Fatalf("unauthorized code = %d, want -32001", unauth.Code)
	}
	forbidden := MapNIP86Error(http.StatusForbidden, errors.New("forbidden"))
	if forbidden.Code != -32001 {
		t.Fatalf("forbidden code = %d, want -32001", forbidden.Code)
	}
	notFound := MapNIP86Error(http.StatusNotFound, errors.New("unknown method"))
	if notFound.Code != -32601 {
		t.Fatalf("not found code = %d, want -32601", notFound.Code)
	}
}
