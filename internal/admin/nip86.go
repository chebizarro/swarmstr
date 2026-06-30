package admin

import (
	"net/http"
	"strings"

	"metiq/internal/nostr/nip86"
)

type NIP86Options struct {
	Enabled  bool
	Path     string
	RelayURL string
	Store    nip86.ManagementStore
}

func mountNIP86(mux *http.ServeMux, opts ServerOptions) {
	if !opts.NIP86.Enabled {
		return
	}
	path := strings.TrimSpace(opts.NIP86.Path)
	if path == "" {
		path = "/nip86"
	}
	mux.Handle(path, nip86.NewHandler(opts.NIP86.Store, opts.NIP86.RelayURL))
}
