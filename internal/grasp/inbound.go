package grasp

import (
	"fmt"
	"strings"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/events"
)

type RepoRef struct {
	Addr        string `json:"addr,omitempty"`
	ID          string `json:"id,omitempty"`
	OwnerPubKey string `json:"owner_pubkey,omitempty"`
}

type InboundEventType string

const (
	InboundEventPatch    InboundEventType = "patch"
	InboundEventPR       InboundEventType = "pull_request"
	InboundEventPRUpdate InboundEventType = "pull_request_update"
	InboundEventIssue    InboundEventType = "issue"
	InboundEventStatus   InboundEventType = "status"
	InboundStatusOpen    string           = "open"
	InboundStatusApplied string           = "applied"
	InboundStatusClosed  string           = "closed"
	InboundStatusDraft   string           = "draft"
)

// InboundEvent is the normalized form of a repo-targeted NIP-34 event that an
// inbox consumer can route into an agent session.
type InboundEvent struct {
	Type             InboundEventType `json:"type"`
	Kind             int              `json:"kind"`
	Status           string           `json:"status,omitempty"`
	Repo             RepoRef          `json:"repo"`
	AuthorPubKey     string           `json:"author_pubkey"`
	Subject          string           `json:"subject,omitempty"`
	Body             string           `json:"body,omitempty"`
	Labels           []string         `json:"labels,omitempty"`
	CommitID         string           `json:"commit_id,omitempty"`
	CommitTip        string           `json:"commit_tip,omitempty"`
	CloneURLs        []string         `json:"clone_urls,omitempty"`
	BranchName       string           `json:"branch_name,omitempty"`
	MergeBase        string           `json:"merge_base,omitempty"`
	MergeCommit      string           `json:"merge_commit,omitempty"`
	AppliedCommitIDs []string         `json:"applied_commit_ids,omitempty"`
	RootEventID      string           `json:"root_event_id,omitempty"`
	ReplyEventID     string           `json:"reply_event_id,omitempty"`
	MentionEventIDs  []string         `json:"mention_event_ids,omitempty"`
	EventID          string           `json:"event_id"`
	CreatedAt        int64            `json:"created_at"`
}

// SplitRepoAddr decodes a NIP-34 repository address into its components. It is
// forgiving on malformed input and returns any partial fields it can recover.
func SplitRepoAddr(addr string) (RepoRef, error) {
	ref := RepoRef{Addr: strings.TrimSpace(addr)}
	if ref.Addr == "" {
		return ref, fmt.Errorf("repo address is empty")
	}
	parts := strings.SplitN(ref.Addr, ":", 3)
	if len(parts) >= 2 {
		ref.OwnerPubKey = strings.TrimSpace(parts[1])
	}
	if len(parts) == 3 {
		ref.ID = strings.TrimSpace(parts[2])
	}
	if err := ValidateRepoAddr(ref.Addr); err != nil {
		return ref, err
	}
	return ref, nil
}

// ParseInboundEvent decodes the repo-targeted NIP-34 event kinds metiq can
// receive from external GRASP publishers.
func ParseInboundEvent(ev *nostr.Event) (InboundEvent, error) {
	if ev == nil {
		return InboundEvent{}, fmt.Errorf("grasp inbound event is nil")
	}
	out := InboundEvent{
		Kind:         int(ev.Kind),
		AuthorPubKey: strings.TrimSpace(ev.PubKey.Hex()),
		Body:         ev.Content,
		EventID:      strings.TrimSpace(ev.ID.Hex()),
		CreatedAt:    int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		key := strings.TrimSpace(tag[0])
		value := strings.TrimSpace(tag[1])
		switch key {
		case "a":
			if out.Repo.Addr == "" {
				out.Repo.Addr = value
			}
		case "subject":
			if out.Subject == "" {
				out.Subject = value
			}
		case "t":
			if value != "" {
				out.Labels = append(out.Labels, value)
			}
		case "commit":
			if out.CommitID == "" {
				out.CommitID = value
			}
		case "c":
			if out.CommitTip == "" {
				out.CommitTip = value
			}
		case "clone":
			for _, cloneURL := range tag[1:] {
				if cloneURL = strings.TrimSpace(cloneURL); cloneURL != "" {
					out.CloneURLs = append(out.CloneURLs, cloneURL)
				}
			}
		case "branch-name":
			if out.BranchName == "" {
				out.BranchName = value
			}
		case "merge-base":
			if out.MergeBase == "" {
				out.MergeBase = value
			}
		case "merge-commit":
			if out.MergeCommit == "" {
				out.MergeCommit = value
			}
		case "applied-as-commits":
			for _, commitID := range tag[1:] {
				if commitID = strings.TrimSpace(commitID); commitID != "" {
					out.AppliedCommitIDs = append(out.AppliedCommitIDs, commitID)
				}
			}
		case "e":
			parseInboundEventRefTag(&out, tag)
		}
	}
	if repo, _ := SplitRepoAddr(out.Repo.Addr); repo.Addr != "" {
		out.Repo = repo
	}
	switch ev.Kind {
	case nostr.Kind(events.KindPatch):
		out.Type = InboundEventPatch
	case nostr.Kind(events.KindPR):
		out.Type = InboundEventPR
	case nostr.Kind(events.KindPRUpdate):
		out.Type = InboundEventPRUpdate
	case nostr.Kind(events.KindIssue):
		out.Type = InboundEventIssue
	case nostr.Kind(events.KindStatusOpen):
		out.Type = InboundEventStatus
		out.Status = InboundStatusOpen
	case nostr.Kind(events.KindStatusApplied):
		out.Type = InboundEventStatus
		out.Status = InboundStatusApplied
	case nostr.Kind(events.KindStatusClosed):
		out.Type = InboundEventStatus
		out.Status = InboundStatusClosed
	case nostr.Kind(events.KindStatusDraft):
		out.Type = InboundEventStatus
		out.Status = InboundStatusDraft
	default:
		return InboundEvent{}, fmt.Errorf("unsupported grasp inbound kind %d", ev.Kind)
	}
	return out, nil
}

func parseInboundEventRefTag(out *InboundEvent, tag nostr.Tag) {
	if out == nil || len(tag) < 2 {
		return
	}
	eventID := strings.TrimSpace(tag[1])
	if eventID == "" {
		return
	}
	marker := ""
	if len(tag) >= 4 {
		marker = strings.TrimSpace(tag[3])
	}
	switch marker {
	case "root":
		if out.RootEventID == "" {
			out.RootEventID = eventID
		}
	case "reply":
		if out.ReplyEventID == "" {
			out.ReplyEventID = eventID
		}
	case "mention":
		out.MentionEventIDs = append(out.MentionEventIDs, eventID)
	default:
		if out.RootEventID == "" {
			out.RootEventID = eventID
			return
		}
		if out.ReplyEventID == "" {
			out.ReplyEventID = eventID
			return
		}
		out.MentionEventIDs = append(out.MentionEventIDs, eventID)
	}
}
