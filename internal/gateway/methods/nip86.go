package methods

import (
	"errors"
	"net/http"
	"strings"
)

type NIP86Error struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func MapNIP86Error(status int, err error) NIP86Error {
	msg := "internal error"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		msg = err.Error()
	}

	code := -32000
	switch status {
	case http.StatusBadRequest:
		code = -32602 // invalid params
	case http.StatusNotFound:
		code = -32601 // method not found
	case http.StatusUnauthorized, http.StatusForbidden:
		code = -32001 // unauthorized
	case http.StatusConflict:
		code = -32010 // precondition failed
	}

	out := NIP86Error{Code: code, Message: msg}
	type codedDataError interface {
		ErrorCode() int
		ErrorData() map[string]any
	}
	var coded codedDataError
	if errors.As(err, &coded) {
		out.Code = coded.ErrorCode()
		out.Data = coded.ErrorData()
	}
	return out
}
