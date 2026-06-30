package commitments

import "time"

// ExtractionRequest is queued for asynchronous commitment extraction.
type ExtractionRequest struct {
	SessionID string
	TurnID    string
	Text      string
	Channel   string
	To        string
}

// Runtime owns asynchronous extraction into the durable store.
type Runtime struct {
	Extractor Extractor
	Store     *Store
	Config    Config
	Now       func() time.Time
	queue     chan ExtractionRequest
}

func NewRuntime(store *Store, extractor Extractor, cfg Config) *Runtime {
	cfg = cfg.withDefaults()
	if store == nil {
		store = NewStore()
	}
	extractor.Config = cfg
	return &Runtime{Extractor: extractor, Store: store, Config: cfg, queue: make(chan ExtractionRequest, 64)}
}

func (r *Runtime) Start() {
	go func() {
		for req := range r.queue {
			_ = r.Process(req)
		}
	}()
}

func (r *Runtime) Enqueue(req ExtractionRequest) bool {
	if !r.Config.withDefaults().Enabled {
		return false
	}
	select {
	case r.queue <- req:
		return true
	default:
		return false
	}
}

func (r *Runtime) Process(req ExtractionRequest) error {
	if !r.Config.withDefaults().Enabled {
		return nil
	}
	items, err := r.Extractor.Extract(req.SessionID, req.TurnID, req.Text)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Channel = req.Channel
		items[i].To = req.To
		items[i].DeliverySessionID = req.SessionID
	}
	r.Store.Add(items...)
	return nil
}
