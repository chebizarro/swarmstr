package nip51

// Cascadia d-tag prefixes for parameterized NIP-51 lists.
const (
	CascadiaOperatorsPrefix    = "operators:"
	CascadiaApproversPrefix    = "approvers:"
	CascadiaCapabilitiesPrefix = "capabilities:"
	CascadiaDependenciesPrefix = "dependencies:"
	CascadiaArtifactsPrefix    = "artifacts:"
	CascadiaMembersPrefix      = "members:"
	CascadiaRelaysPrefix       = "relays:"
)

func CascadiaOperatorsDTag(scope string) string {
	return CascadiaOperatorsPrefix + scope
}

func CascadiaApproversDTag(service string) string {
	return CascadiaApproversPrefix + service
}

func CascadiaCapabilitiesDTag(agentID string) string {
	return CascadiaCapabilitiesPrefix + agentID
}

func CascadiaDependenciesDTag(service string) string {
	return CascadiaDependenciesPrefix + service
}

func CascadiaArtifactsDTag(release string) string {
	return CascadiaArtifactsPrefix + release
}

func CascadiaMembersDTag(project string) string {
	return CascadiaMembersPrefix + project
}

func CascadiaRelaysDTag(purpose string) string {
	return CascadiaRelaysPrefix + purpose
}

func NewCascadiaOperatorsList(pubkey, scope string, operators []string) *List {
	return newCascadiaPeopleList(pubkey, CascadiaOperatorsDTag(scope), operators)
}

func NewCascadiaApproversList(pubkey, service string, approvers []string) *List {
	return newCascadiaPeopleList(pubkey, CascadiaApproversDTag(service), approvers)
}

func NewCascadiaCapabilitiesList(pubkey, agentID string, capabilities []string) *List {
	return newCascadiaTagList(pubkey, KindCurationSet, CascadiaCapabilitiesDTag(agentID), "t", capabilities)
}

func NewCascadiaDependenciesList(pubkey, service string, dependencies []string) *List {
	return newCascadiaTagList(pubkey, KindFollowSet, CascadiaDependenciesDTag(service), "t", dependencies)
}

func NewCascadiaArtifactsList(pubkey, release string, artifacts []string) *List {
	return newCascadiaTagList(pubkey, KindBookmarkSet, CascadiaArtifactsDTag(release), "a", artifacts)
}

func NewCascadiaMembersList(pubkey, project string, members []string) *List {
	return newCascadiaPeopleList(pubkey, CascadiaMembersDTag(project), members)
}

func NewCascadiaRelaysList(pubkey, purpose string, relays []string) *List {
	return NewRelaySetList(pubkey, CascadiaRelaysDTag(purpose), relays)
}

func (s *ListStore) IsCascadiaOperator(ownerPubkey, scope, targetPubkey string) bool {
	return s.containsEntry(ownerPubkey, KindPeopleList, CascadiaOperatorsDTag(scope), "p", targetPubkey)
}

func (s *ListStore) IsCascadiaApprover(ownerPubkey, service, targetPubkey string) bool {
	return s.containsEntry(ownerPubkey, KindPeopleList, CascadiaApproversDTag(service), "p", targetPubkey)
}

func (s *ListStore) HasCascadiaCapability(ownerPubkey, agentID, capability string) bool {
	return s.containsEntry(ownerPubkey, KindCurationSet, CascadiaCapabilitiesDTag(agentID), "t", capability)
}

func (s *ListStore) CascadiaDependencies(ownerPubkey, service string) []string {
	return s.valuesByTag(ownerPubkey, KindFollowSet, CascadiaDependenciesDTag(service), "t")
}

func (s *ListStore) CascadiaArtifacts(ownerPubkey, release string) []string {
	return s.valuesByTag(ownerPubkey, KindBookmarkSet, CascadiaArtifactsDTag(release), "a")
}

func (s *ListStore) IsCascadiaMember(ownerPubkey, project, targetPubkey string) bool {
	return s.containsEntry(ownerPubkey, KindPeopleList, CascadiaMembersDTag(project), "p", targetPubkey)
}

func (s *ListStore) CascadiaRelays(ownerPubkey, purpose string) []string {
	l, ok := s.Get(ownerPubkey, KindRelaySet, CascadiaRelaysDTag(purpose))
	if !ok {
		return nil
	}
	return RelaysFromList(l)
}

func newCascadiaPeopleList(pubkey, dtag string, pubkeys []string) *List {
	return newCascadiaTagList(pubkey, KindPeopleList, dtag, "p", pubkeys)
}

func newCascadiaTagList(pubkey string, kind int, dtag, tag string, values []string) *List {
	entries := make([]ListEntry, 0, len(values))
	for _, value := range values {
		if value != "" {
			entries = append(entries, ListEntry{Tag: tag, Value: value})
		}
	}
	return &List{Kind: kind, DTag: dtag, PubKey: pubkey, Entries: entries}
}

func (s *ListStore) containsEntry(ownerPubkey string, kind int, dtag, tag, value string) bool {
	l, ok := s.Get(ownerPubkey, kind, dtag)
	if !ok {
		return false
	}
	for _, entry := range l.Entries {
		if entry.Tag == tag && entry.Value == value {
			return true
		}
	}
	return false
}

func (s *ListStore) valuesByTag(ownerPubkey string, kind int, dtag, tag string) []string {
	l, ok := s.Get(ownerPubkey, kind, dtag)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(l.Entries))
	for _, entry := range l.Entries {
		if entry.Tag == tag && entry.Value != "" {
			values = append(values, entry.Value)
		}
	}
	return values
}
