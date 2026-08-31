package nip34

import (
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

const (
	KindRepositoryAnnouncement nostr.Kind = nostr.Kind(events.KindRepoAnnouncement)
	KindRepositoryState        nostr.Kind = nostr.Kind(events.KindRepoState)
	KindPatch                  nostr.Kind = nostr.Kind(events.KindPatch)
	KindPullRequest            nostr.Kind = nostr.Kind(events.KindPR)
	KindPullRequestUpdate      nostr.Kind = nostr.Kind(events.KindPRUpdate)
	KindIssue                  nostr.Kind = nostr.Kind(events.KindIssue)
	KindGraspList              nostr.Kind = nostr.Kind(events.KindNIP34GraspList)
	KindStatusOpen             nostr.Kind = nostr.Kind(events.KindStatusOpen)
	KindStatusApplied          nostr.Kind = nostr.Kind(events.KindStatusApplied)
	KindStatusClosed           nostr.Kind = nostr.Kind(events.KindStatusClosed)
	KindStatusDraft            nostr.Kind = nostr.Kind(events.KindStatusDraft)
)

type RepositoryCoordinate struct {
	Owner      nostr.PubKey
	Identifier string
}

func (r RepositoryCoordinate) String() string {
	return fmt.Sprintf("30617:%s:%s", r.Owner.Hex(), r.Identifier)
}

func ParseRepositoryCoordinate(raw string) (RepositoryCoordinate, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[0] != "30617" || parts[2] == "" || strings.ContainsAny(parts[2], "\x00\r\n") {
		return RepositoryCoordinate{}, fmt.Errorf("invalid NIP-34 repository coordinate")
	}
	if parts[1] != strings.ToLower(parts[1]) {
		return RepositoryCoordinate{}, fmt.Errorf("repository owner must be canonical lowercase hex")
	}
	owner, err := nostr.PubKeyFromHex(parts[1])
	if err != nil {
		return RepositoryCoordinate{}, fmt.Errorf("invalid repository owner: %w", err)
	}
	return RepositoryCoordinate{Owner: owner, Identifier: parts[2]}, nil
}

type Upstream struct {
	Repository string
	RelayHint  string
	Author     nostr.PubKey
}

type RepositoryAnnouncement struct {
	Identifier           string
	Name                 string
	Description          string
	Web                  []string
	Clone                []string
	Relays               []string
	EarliestUniqueCommit string
	Maintainers          []nostr.PubKey
	Upstream             *Upstream
	Topics               []string
	Event                nostr.Event
}

func (r RepositoryAnnouncement) Coordinate() RepositoryCoordinate {
	return RepositoryCoordinate{Owner: r.Event.PubKey, Identifier: r.Identifier}
}

type RepositoryState struct {
	Identifier string
	HEAD       string
	Refs       map[string]string
	Event      nostr.Event
}

type EventReference struct {
	ID        nostr.ID
	RelayHint string
	Author    *nostr.PubKey
}

type CommitMetadata struct {
	CommitID       string
	ParentCommitID string
	PGPSignature   string
	CommitterName  string
	CommitterEmail string
	Timestamp      string
	TimezoneOffset string
}

type Patch struct {
	Repository           RepositoryCoordinate
	EarliestUniqueCommit string
	Recipients           []nostr.PubKey
	Root                 bool
	RootRevision         bool
	Reply                *EventReference
	Commit               CommitMetadata
	Content              string
	Warnings             []string
	Event                nostr.Event
}

type PullRequest struct {
	Repository           RepositoryCoordinate
	EarliestUniqueCommit string
	Recipients           []nostr.PubKey
	Subject              string
	Labels               []string
	Tip                  string
	Clone                []string
	BranchName           string
	RevisionOf           *nostr.ID
	MergeBase            string
	Content              string
	Warnings             []string
	Event                nostr.Event
}

type PullRequestUpdate struct {
	Repository           RepositoryCoordinate
	EarliestUniqueCommit string
	Recipients           []nostr.PubKey
	PullRequestID        nostr.ID
	PullRequestAuthor    nostr.PubKey
	Tip                  string
	Clone                []string
	MergeBase            string
	Warnings             []string
	Event                nostr.Event
}

type Issue struct {
	Repository RepositoryCoordinate
	Recipients []nostr.PubKey
	Subject    string
	Labels     []string
	Content    string
	Warnings   []string
	Event      nostr.Event
}

type Status struct {
	Kind                 nostr.Kind
	Repository           *RepositoryCoordinate
	Root                 nostr.ID
	AcceptedRevision     *nostr.ID
	Recipients           []nostr.PubKey
	EarliestUniqueCommit string
	AppliedOrMerged      []EventReference
	MergeCommit          string
	AppliedAsCommits     []string
	Content              string
	Event                nostr.Event
}

type GraspList struct {
	Servers []string
	Event   nostr.Event
}
