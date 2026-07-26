package board

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// htmlDocument is the stored source for an html widget. The board snapshot
// carries only metadata; approved bytes stay pinned here so grant preservation
// can compare content hashes.
type htmlDocument struct {
	HTML       string
	SHA256     string
	Revision   int
	GrantState string
	Declared   *Declared
	// ViewGeneration pins the widget instance the document was put under so
	// view tickets minted against it go stale on any re-put.
	ViewGeneration string
}

// McpAppDescriptor pins the provenance of an MCP-App widget so views can be
// re-minted after the originating view expires.
type McpAppDescriptor struct {
	ServerName    string
	ToolName      string
	UIResourceURI string
	ToolCallID    string
}

// mcpAppDocument is the stored source for an mcp-app widget.
type mcpAppDocument struct {
	Descriptor  McpAppDescriptor
	Interactive bool
	Revision    int
	InstanceID  string
	GrantState  string
	Declared    *Declared
}

// McpAppView is the read-back of a pinned mcp-app widget document.
type McpAppView struct {
	Descriptor  McpAppDescriptor
	Interactive bool
	Revision    int
	InstanceID  string
	GrantState  string
	Declared    *Declared
}

// CanvasDocDescriptor pins the referenced canvas document id for a canvas-doc
// widget (swarmstr-5p0v item 1).
type CanvasDocDescriptor struct {
	DocID string
}

// canvasDocument is the stored source for a canvas-doc widget.
type canvasDocument struct {
	DocID      string
	Revision   int
	GrantState string
}

// CanvasDocView is the read-back of a pinned canvas-doc widget document.
type CanvasDocView struct {
	DocID      string
	Revision   int
	GrantState string
}

type storedBoard struct {
	snapshot        Snapshot
	documents       map[string]*htmlDocument
	appDocuments    map[string]*mcpAppDocument
	canvasDocuments map[string]*canvasDocument
}

// Store is the in-memory per-sessionKey board store. All methods are safe for
// concurrent use. Boards are process-local state, mirroring the OpenClaw
// in-memory board store; persistence is a follow-up.
type Store struct {
	mu     sync.Mutex
	boards map[string]*storedBoard
	// ticketSecret signs view tickets; per-process so tickets never survive
	// a restart. now is injectable for ticket expiry tests.
	ticketSecret []byte
	now          func() time.Time
}

// NewStore returns an empty board store.
func NewStore() *Store {
	return &Store{
		boards:       map[string]*storedBoard{},
		ticketSecret: newTicketSecret(),
		now:          time.Now,
	}
}

func emptySnapshot(sessionKey string) Snapshot {
	return Snapshot{SessionKey: sessionKey, Revision: 0, Tabs: []Tab{}, Widgets: []Widget{}}
}

// GetSnapshot returns a deep copy of the board for sessionKey (empty board
// when none exists).
func (s *Store) GetSnapshot(sessionKey string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.boards[sessionKey]; ok {
		return CloneSnapshot(b.snapshot)
	}
	return emptySnapshot(sessionKey)
}

// HasWidget reports whether the named widget exists on the sessionKey board.
func (s *Store) HasWidget(sessionKey, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[sessionKey]
	if !ok {
		return false
	}
	for _, w := range b.snapshot.Widgets {
		if w.Name == name {
			return true
		}
	}
	return false
}

// ApplyOps applies layout operations and bumps the board revision when at
// least one op was supplied. Removing the final tab and widget deletes the
// board entirely.
func (s *Store) ApplyOps(sessionKey string, ops []Op) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.boards[sessionKey]
	snapshot := emptySnapshot(sessionKey)
	if current != nil {
		snapshot = current.snapshot
	}
	if len(ops) == 0 {
		return CloneSnapshot(snapshot), nil
	}
	nextLayout, err := applyOps(layout{Tabs: snapshot.Tabs, Widgets: snapshot.Widgets}, ops)
	if err != nil {
		return Snapshot{}, err
	}
	next := Snapshot{SessionKey: sessionKey, Revision: snapshot.Revision + 1, Tabs: nextLayout.Tabs, Widgets: nextLayout.Widgets}
	remaining := map[string]bool{}
	for _, w := range next.Widgets {
		remaining[w.Name] = true
	}
	documents := map[string]*htmlDocument{}
	appDocuments := map[string]*mcpAppDocument{}
	canvasDocuments := map[string]*canvasDocument{}
	if current != nil {
		for name, doc := range current.documents {
			if remaining[name] {
				documents[name] = doc
			}
		}
		for name, doc := range current.appDocuments {
			if remaining[name] {
				appDocuments[name] = doc
			}
		}
		for name, doc := range current.canvasDocuments {
			if remaining[name] {
				canvasDocuments[name] = doc
			}
		}
	}
	if len(next.Tabs) == 0 && len(next.Widgets) == 0 {
		delete(s.boards, sessionKey)
	} else {
		s.boards[sessionKey] = &storedBoard{snapshot: next, documents: documents, appDocuments: appDocuments, canvasDocuments: canvasDocuments}
	}
	return CloneSnapshot(next), nil
}

// PutContent is the materialized widget content accepted by the store.
type PutContent struct {
	Kind       string
	HTML       string
	PluginKind string
	Props      map[string]any
	// McpApp pins the descriptor for mcp-app content (resolved from an
	// active MCP-App view by the handler before the store sees it).
	McpApp      *McpAppDescriptor
	Interactive bool
	// CanvasDoc pins the referenced canvas document for canvas-doc content.
	CanvasDoc *CanvasDocDescriptor
}

// PutPlacement optionally targets a tab, size preset, and anchor widget.
type PutPlacement struct {
	TabID string
	Size  string
	After string
}

// PutParams describes one board.widget.put request after wire validation.
type PutParams struct {
	SessionKey   string
	Name         string
	Title        string
	Content      PutContent
	Presentation string
	HeightMode   string
	Placement    *PutPlacement
	Declared     *Declared
}

func newInstanceID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// PutWidget creates or replaces a widget, bumping widget and board revisions.
// HTML grants are frozen to approved bytes: a put that re-submits identical
// content with a subset declaration keeps its granted state; anything else
// returns to pending (or none when nothing is declared).
func (s *Store) PutWidget(params PutParams) (Snapshot, error) {
	if !widgetNamePattern.MatchString(params.Name) {
		return Snapshot{}, errInvalid("invalid board widget name: %s", params.Name)
	}
	switch params.Content.Kind {
	case ContentKindHTML:
		if len(params.Content.HTML) > MaxWidgetHTMLBytes {
			return Snapshot{}, errInvalid("board widget HTML exceeds %d UTF-8 bytes", MaxWidgetHTMLBytes)
		}
	case ContentKindMcpApp:
		if params.Content.McpApp == nil || params.Content.McpApp.ServerName == "" ||
			params.Content.McpApp.ToolName == "" || params.Content.McpApp.UIResourceURI == "" {
			return Snapshot{}, errInvalid("board mcp-app widget requires a resolved view descriptor")
		}
	case ContentKindCanvasDoc:
		if params.Declared != nil {
			return Snapshot{}, errInvalid("canvas-doc widgets do not accept sandbox capability declarations")
		}
		if params.Content.CanvasDoc == nil || !canvasDocIDPattern.MatchString(params.Content.CanvasDoc.DocID) {
			return Snapshot{}, errInvalid("board canvas-doc widget requires a valid docId")
		}
	case ContentKindPlugin:
		if params.Declared != nil {
			return Snapshot{}, errInvalid("trusted plugin widgets do not accept sandbox capability declarations")
		}
		if !pluginKindPattern.MatchString(params.Content.PluginKind) {
			return Snapshot{}, errInvalid("invalid board plugin kind: %s", params.Content.PluginKind)
		}
		props, err := json.Marshal(params.Content.Props)
		if err != nil {
			return Snapshot{}, errInvalid("board plugin widget props must be JSON serializable")
		}
		if len(props) > MaxPluginPropBytes {
			return Snapshot{}, errInvalid("board plugin widget props exceed %d UTF-8 bytes", MaxPluginPropBytes)
		}
	default:
		return Snapshot{}, errInvalid("unsupported board widget content kind: %s", params.Content.Kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.boards[params.SessionKey]
	prior := emptySnapshot(params.SessionKey)
	if current != nil {
		prior = current.snapshot
	}

	l := normalizeLayout(layout{Tabs: prior.Tabs, Widgets: prior.Widgets})
	if len(l.Tabs) == 0 {
		l.Tabs = append(l.Tabs, Tab{TabID: "main", Title: "Main", Position: 0, ChatDock: "right"})
	}
	existingIdx := findWidget(&l, params.Name)
	var existing *Widget
	if existingIdx >= 0 {
		w := cloneWidget(l.Widgets[existingIdx])
		existing = &w
	}
	if existing == nil && len(l.Widgets) >= MaxWidgets {
		return Snapshot{}, errInvalid("board cannot contain more than %d widgets", MaxWidgets)
	}

	tabID := ""
	if params.Placement != nil {
		tabID = params.Placement.TabID
	}
	if tabID == "" && existing != nil {
		tabID = existing.TabID
	}
	if tabID == "" {
		tabID = l.Tabs[0].TabID
	}
	if findTab(&l, tabID) < 0 {
		return Snapshot{}, errNotFound("board tab not found: %s", tabID)
	}

	sizeKey := "md"
	sizeExplicit := false
	if params.Placement != nil && params.Placement.Size != "" {
		if _, ok := SizePresets[params.Placement.Size]; !ok {
			return Snapshot{}, errInvalid("invalid board widget size: %s", params.Placement.Size)
		}
		sizeKey = params.Placement.Size
		sizeExplicit = true
	}
	size := SizePresets[sizeKey]

	widgetRevision := 1
	if existing != nil {
		widgetRevision = existing.Revision + 1
	}

	var declared *Declared
	if params.Content.Kind != ContentKindPlugin && params.Content.Kind != ContentKindCanvasDoc {
		declared = normalizeDeclaredCapabilities(params.Declared)
	}
	summary := declaredSummaryLines(declared)

	contentSHA := ""
	if params.Content.Kind == ContentKindHTML {
		sum := sha256.Sum256([]byte(params.Content.HTML))
		contentSHA = hex.EncodeToString(sum[:])
	}

	var existingDoc *htmlDocument
	if current != nil {
		existingDoc = current.documents[params.Name]
	}
	grantedSHA := ""
	if existingDoc != nil && existingDoc.GrantState == GrantGranted {
		grantedSHA = existingDoc.SHA256
	}
	// Grant scope: only html→html puts can preserve a grant in this slice.
	grantScopeMatches := existingDoc == nil || params.Content.Kind == ContentKindHTML
	preservesGrant := declared != nil &&
		grantScopeMatches &&
		existing != nil && existing.GrantState == GrantGranted &&
		params.Content.Kind == ContentKindHTML && contentSHA == grantedSHA &&
		declarationIsSubset(declared, existing.Declared)

	grantState := GrantNone
	switch {
	case params.Content.Kind == ContentKindPlugin:
		grantState = GrantNone
	case preservesGrant:
		grantState = GrantGranted
	case summary != nil:
		grantState = GrantPending
	}

	instanceID := newInstanceID()
	widget := Widget{
		Name:        params.Name,
		TabID:       tabID,
		ContentKind: params.Content.Kind,
		SizeW:       size.W,
		SizeH:       size.H,
		GrantState:  grantState,
		Revision:    widgetRevision,
	}
	if params.Title != "" {
		widget.Title = params.Title
	} else if existing != nil {
		widget.Title = existing.Title
	}
	if params.Presentation != "" {
		widget.Presentation = params.Presentation
	} else if existing != nil {
		widget.Presentation = existing.Presentation
	}
	if params.HeightMode != "" {
		widget.HeightMode = params.HeightMode
	} else if existing != nil {
		widget.HeightMode = existing.HeightMode
	}
	if !sizeExplicit && existing != nil {
		widget.SizeW = existing.SizeW
		widget.SizeH = existing.SizeH
	}
	if params.Content.Kind == ContentKindPlugin {
		widget.PluginKind = params.Content.PluginKind
		if params.Content.Props != nil {
			props := make(map[string]any, len(params.Content.Props))
			for k, v := range params.Content.Props {
				props[k] = v
			}
			widget.Props = props
		}
	} else {
		widget.InstanceID = instanceID
	}
	widget.DeclaredSummary = summary
	if summary != nil {
		widget.Declared = cloneDeclared(declared)
	}

	after := ""
	explicitPlacement := false
	if params.Placement != nil {
		after = params.Placement.After
		explicitPlacement = params.Placement.TabID != "" || params.Placement.After != ""
	}
	if existing != nil {
		widget.Position = existing.Position
		l.Widgets[existingIdx] = widget
		if explicitPlacement {
			if err := moveWidget(&l, widget.Name, tabID, nil, after); err != nil {
				return Snapshot{}, err
			}
		}
	} else {
		widget.Position = len(l.Widgets)
		l.Widgets = append(l.Widgets, widget)
		if err := moveWidget(&l, widget.Name, tabID, nil, after); err != nil {
			return Snapshot{}, err
		}
	}
	l = normalizeLayout(l)

	next := Snapshot{SessionKey: params.SessionKey, Revision: prior.Revision + 1, Tabs: l.Tabs, Widgets: l.Widgets}
	documents := map[string]*htmlDocument{}
	appDocuments := map[string]*mcpAppDocument{}
	canvasDocuments := map[string]*canvasDocument{}
	if current != nil {
		for name, doc := range current.documents {
			documents[name] = doc
		}
		for name, doc := range current.appDocuments {
			appDocuments[name] = doc
		}
		for name, doc := range current.canvasDocuments {
			canvasDocuments[name] = doc
		}
	}
	switch params.Content.Kind {
	case ContentKindHTML:
		documents[params.Name] = &htmlDocument{
			HTML:           params.Content.HTML,
			SHA256:         contentSHA,
			Revision:       widgetRevision,
			GrantState:     grantState,
			Declared:       cloneDeclared(declared),
			ViewGeneration: instanceID,
		}
		delete(appDocuments, params.Name)
	case ContentKindMcpApp:
		descriptor := *params.Content.McpApp
		appDocuments[params.Name] = &mcpAppDocument{
			Descriptor:  descriptor,
			Interactive: params.Content.Interactive,
			Revision:    widgetRevision,
			InstanceID:  instanceID,
			GrantState:  grantState,
			Declared:    cloneDeclared(declared),
		}
		delete(documents, params.Name)
		delete(canvasDocuments, params.Name)
	case ContentKindCanvasDoc:
		canvasDocuments[params.Name] = &canvasDocument{
			DocID:      params.Content.CanvasDoc.DocID,
			Revision:   widgetRevision,
			GrantState: grantState,
		}
		delete(documents, params.Name)
		delete(appDocuments, params.Name)
	default:
		delete(documents, params.Name)
		delete(appDocuments, params.Name)
		delete(canvasDocuments, params.Name)
	}
	s.boards[params.SessionKey] = &storedBoard{snapshot: next, documents: documents, appDocuments: appDocuments, canvasDocuments: canvasDocuments}
	return CloneSnapshot(next), nil
}

// ReadWidgetCanvasDoc returns the pinned canvas-doc document for a widget.
func (s *Store) ReadWidgetCanvasDoc(sessionKey, name string) (CanvasDocView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[sessionKey]
	if !ok {
		return CanvasDocView{}, false
	}
	doc, ok := b.canvasDocuments[name]
	if !ok {
		return CanvasDocView{}, false
	}
	return CanvasDocView{DocID: doc.DocID, Revision: doc.Revision, GrantState: doc.GrantState}, true
}

// ReadWidgetMcpApp returns the pinned mcp-app document for a widget.
func (s *Store) ReadWidgetMcpApp(sessionKey, name string) (McpAppView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.boards[sessionKey]
	if !ok {
		return McpAppView{}, false
	}
	doc, ok := b.appDocuments[name]
	if !ok {
		return McpAppView{}, false
	}
	return McpAppView{
		Descriptor:  doc.Descriptor,
		Interactive: doc.Interactive,
		Revision:    doc.Revision,
		InstanceID:  doc.InstanceID,
		GrantState:  doc.GrantState,
		Declared:    cloneDeclared(doc.Declared),
	}, true
}

// Grant resolves a pending capability grant. The caller must present the
// widget revision and instance it saw so a concurrent re-put can never be
// granted retroactively.
func (s *Store) Grant(sessionKey, name, decision string, revision int, instanceID string) (Snapshot, error) {
	if decision != GrantGranted && decision != GrantRejected {
		return Snapshot{}, errInvalid("invalid board grant decision: %s", decision)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.boards[sessionKey]
	if !ok {
		return Snapshot{}, errNotFound("board widget not found: %s", name)
	}
	snapshot := CloneSnapshot(current.snapshot)
	idx := -1
	for i := range snapshot.Widgets {
		if snapshot.Widgets[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Snapshot{}, errNotFound("board widget not found: %s", name)
	}
	widget := &snapshot.Widgets[idx]
	if widget.Revision != revision {
		return Snapshot{}, errConflict("board widget revision changed: %s is revision %d, not %d", name, widget.Revision, revision)
	}
	if widget.InstanceID != "" && widget.InstanceID != instanceID {
		return Snapshot{}, errConflict("board widget instance changed: %s", name)
	}
	if widget.GrantState != GrantPending {
		return Snapshot{}, errInvalid("board widget grant is not pending: %s", name)
	}
	widget.GrantState = decision
	snapshot.Revision++
	if doc, ok := current.documents[name]; ok {
		doc.GrantState = decision
	}
	if doc, ok := current.appDocuments[name]; ok {
		doc.GrantState = decision
	}
	if doc, ok := current.canvasDocuments[name]; ok {
		doc.GrantState = decision
	}
	current.snapshot = snapshot
	return CloneSnapshot(snapshot), nil
}
