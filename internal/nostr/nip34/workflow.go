package nip34

import (
	"context"
	"fmt"

	nostr "fiatjaf.com/nostr"
)

func BuildStatus(ctx context.Context, signer nostr.Signer, announcement RepositoryAnnouncement, target nostr.Event, status Status) (nostr.Event, error) {
	if !isStatusKind(status.Kind) {
		return nostr.Event{}, fmt.Errorf("invalid NIP-34 status kind %d", status.Kind)
	}
	verifiedAnnouncement, err := ParseRepositoryAnnouncement(announcement.Event)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("invalid repository announcement: %w", err)
	}
	repo, rootAuthor, err := collaborationTarget(target)
	if err != nil {
		return nostr.Event{}, err
	}
	if repo != verifiedAnnouncement.Coordinate() {
		return nostr.Event{}, fmt.Errorf("target belongs to a different repository")
	}
	if signer == nil {
		return nostr.Event{}, fmt.Errorf("NIP-34 signer required")
	}
	author, err := signer.GetPublicKey(ctx)
	if err != nil {
		return nostr.Event{}, err
	}
	if !authorizedStatusAuthor(author, verifiedAnnouncement, rootAuthor) {
		return nostr.Event{}, fmt.Errorf("status signer is not target author or repository maintainer")
	}
	tags := nostr.Tags{{"e", target.ID.Hex(), "", "root"}}
	if status.AcceptedRevision != nil {
		tags = append(tags, nostr.Tag{"e", status.AcceptedRevision.Hex(), "", "reply"})
	}
	allRecipients := append([]nostr.PubKey{repo.Owner, rootAuthor}, status.Recipients...)
	for _, recipient := range uniquePubKeys(allRecipients) {
		tags = append(tags, nostr.Tag{"p", recipient.Hex()})
	}
	tags = append(tags, nostr.Tag{"a", repo.String()})
	if status.EarliestUniqueCommit != "" {
		if err := validateGitID(status.EarliestUniqueCommit); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"r", status.EarliestUniqueCommit})
	}
	if status.Kind != KindStatusApplied && (len(status.AppliedOrMerged) > 0 || status.MergeCommit != "" || len(status.AppliedAsCommits) > 0) {
		return nostr.Event{}, fmt.Errorf("applied metadata is only valid for kind 1631")
	}
	for _, reference := range status.AppliedOrMerged {
		tag := nostr.Tag{"q", reference.ID.Hex(), reference.RelayHint}
		if reference.Author != nil {
			tag = append(tag, reference.Author.Hex())
		}
		tags = append(tags, tag)
	}
	if status.MergeCommit != "" {
		if err := validateGitID(status.MergeCommit); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"merge-commit", status.MergeCommit}, nostr.Tag{"r", status.MergeCommit})
	}
	if len(status.AppliedAsCommits) > 0 {
		tag := nostr.Tag{"applied-as-commits"}
		for _, id := range status.AppliedAsCommits {
			if err := validateGitID(id); err != nil {
				return nostr.Event{}, err
			}
			tag = append(tag, id)
			tags = append(tags, nostr.Tag{"r", id})
		}
		tags = append(tags, tag)
	}
	return signEvent(ctx, signer, status.Kind, status.Content, tags)
}

func ParseStatus(event nostr.Event) (Status, error) {
	if !isStatusKind(event.Kind) {
		return Status{}, fmt.Errorf("unexpected NIP-34 status kind %d", event.Kind)
	}
	if err := validateSignedEvent(event, event.Kind); err != nil {
		return Status{}, err
	}
	status := Status{Kind: event.Kind, Content: event.Content, Event: event}
	rootSeen := false
	for _, tag := range event.Tags {
		if len(tag) >= 4 && tag[0] == "e" {
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				return Status{}, fmt.Errorf("invalid status event reference")
			}
			switch tag[3] {
			case "root":
				if rootSeen {
					return Status{}, fmt.Errorf("duplicate status root")
				}
				status.Root = id
				rootSeen = true
			case "reply":
				if status.AcceptedRevision != nil {
					return Status{}, fmt.Errorf("duplicate accepted revision")
				}
				status.AcceptedRevision = &id
			}
		} else if len(tag) > 0 && tag[0] == "p" {
			if len(tag) != 2 {
				return Status{}, fmt.Errorf("invalid status p tag")
			}
			pk, err := parsePubKey(tag[1])
			if err != nil {
				return Status{}, err
			}
			status.Recipients = append(status.Recipients, pk)
		} else if len(tag) >= 2 && tag[0] == "q" {
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				return Status{}, err
			}
			reference := EventReference{ID: id}
			if len(tag) >= 3 {
				reference.RelayHint = tag[2]
			}
			if len(tag) >= 4 && tag[3] != "" {
				pk, err := parsePubKey(tag[3])
				if err != nil {
					return Status{}, err
				}
				reference.Author = &pk
			}
			status.AppliedOrMerged = append(status.AppliedOrMerged, reference)
		}
	}
	if !rootSeen {
		return Status{}, fmt.Errorf("status root event tag required")
	}
	if coordinate, err := optionalSingleton(event.Tags, "a"); err != nil {
		return Status{}, err
	} else if coordinate != "" {
		repo, err := ParseRepositoryCoordinate(coordinate)
		if err != nil {
			return Status{}, err
		}
		status.Repository = &repo
	}
	var err error
	if status.MergeCommit, err = optionalSingleton(event.Tags, "merge-commit"); err != nil {
		return Status{}, err
	}
	if status.MergeCommit != "" {
		if err := validateGitID(status.MergeCommit); err != nil {
			return Status{}, err
		}
	}
	appliedTags := tagsNamed(event.Tags, "applied-as-commits")
	if len(appliedTags) > 1 {
		return Status{}, fmt.Errorf("duplicate applied-as-commits tag")
	}
	if len(appliedTags) == 1 {
		for _, id := range appliedTags[0][1:] {
			if err := validateGitID(id); err != nil {
				return Status{}, err
			}
			status.AppliedAsCommits = append(status.AppliedAsCommits, id)
		}
	}
	if event.Kind != KindStatusApplied && (len(status.AppliedOrMerged) > 0 || status.MergeCommit != "" || len(status.AppliedAsCommits) > 0) {
		return Status{}, fmt.Errorf("applied metadata on non-applied status")
	}
	return status, nil
}

// EffectivePatchRevisionStatus applies NIP-34's rule that non-accepted
// revisions close when the root patch is applied.
func EffectivePatchRevisionStatus(rootStatus Status, revisionRoot nostr.ID) nostr.Kind {
	if rootStatus.Kind == KindStatusApplied && (rootStatus.AcceptedRevision == nil || *rootStatus.AcceptedRevision != revisionRoot) {
		return KindStatusClosed
	}
	return rootStatus.Kind
}

func LatestValidStatus(announcement RepositoryAnnouncement, target nostr.Event, candidates []nostr.Event) (Status, bool) {
	verified, err := ParseRepositoryAnnouncement(announcement.Event)
	if err != nil {
		return Status{}, false
	}
	repo, targetAuthor, err := collaborationTarget(target)
	if err != nil || repo != verified.Coordinate() {
		return Status{}, false
	}
	var latest Status
	found := false
	for _, candidate := range candidates {
		status, err := ParseStatus(candidate)
		if err != nil || status.Root != target.ID || candidate.CreatedAt < target.CreatedAt || !authorizedStatusAuthor(candidate.PubKey, verified, targetAuthor) {
			continue
		}
		if status.Repository != nil && *status.Repository != repo {
			continue
		}
		if !containsPubKey(status.Recipients, repo.Owner) || !containsPubKey(status.Recipients, targetAuthor) {
			continue
		}
		if !found || candidate.CreatedAt > latest.Event.CreatedAt || (candidate.CreatedAt == latest.Event.CreatedAt && candidate.ID.Hex() > latest.Event.ID.Hex()) {
			latest, found = status, true
		}
	}
	return latest, found
}

// CurrentStatus returns the latest authorized status, defaulting to Open as
// required by NIP-34 when no status event exists.
func CurrentStatus(announcement RepositoryAnnouncement, target nostr.Event, candidates []nostr.Event) nostr.Kind {
	if latest, ok := LatestValidStatus(announcement, target, candidates); ok {
		return latest.Kind
	}
	return KindStatusOpen
}

// RelayTargets returns the repository announcement's patch/issue relay routes.
func RelayTargets(announcement RepositoryAnnouncement) []string {
	seen := make(map[string]struct{}, len(announcement.Relays))
	out := make([]string, 0, len(announcement.Relays))
	for _, relay := range announcement.Relays {
		if validateRelayURL(relay) != nil {
			continue
		}
		if _, duplicate := seen[relay]; duplicate {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func BuildGraspList(ctx context.Context, signer nostr.Signer, servers []string) (nostr.Event, error) {
	tags := nostr.Tags{}
	for _, server := range servers {
		if err := validateRelayURL(server); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"g", server})
	}
	return signEvent(ctx, signer, KindGraspList, "", tags)
}

func ParseGraspList(event nostr.Event) (GraspList, error) {
	if err := validateSignedEvent(event, KindGraspList); err != nil {
		return GraspList{}, err
	}
	list := GraspList{Event: event}
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "g" {
			if len(tag) != 2 {
				return GraspList{}, fmt.Errorf("invalid grasp server tag")
			}
			if err := validateRelayURL(tag[1]); err != nil {
				return GraspList{}, err
			}
			list.Servers = append(list.Servers, tag[1])
		}
	}
	return list, nil
}

func LatestGraspList(events []nostr.Event, author nostr.PubKey) (GraspList, bool) {
	var latest GraspList
	found := false
	for _, event := range events {
		if event.PubKey != author {
			continue
		}
		list, err := ParseGraspList(event)
		if err != nil {
			continue
		}
		if !found || event.CreatedAt > latest.Event.CreatedAt || (event.CreatedAt == latest.Event.CreatedAt && event.ID.Hex() > latest.Event.ID.Hex()) {
			latest, found = list, true
		}
	}
	return latest, found
}

func RepositoryAnnouncementFilter(owner nostr.PubKey, identifier string) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{KindRepositoryAnnouncement}, Authors: []nostr.PubKey{owner}, Tags: nostr.TagMap{"d": {identifier}}}
}
func RepositoryStateFilter(owner nostr.PubKey, identifier string) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{KindRepositoryState}, Authors: []nostr.PubKey{owner}, Tags: nostr.TagMap{"d": {identifier}}}
}
func RepositoryActivityFilter(repo RepositoryCoordinate, since nostr.Timestamp) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{KindPatch, KindPullRequest, KindPullRequestUpdate, KindIssue}, Tags: nostr.TagMap{"a": {repo.String()}}, Since: since}
}
func RepositoryStatusFilter(repo RepositoryCoordinate, since nostr.Timestamp) nostr.Filter {
	return nostr.Filter{Kinds: statusKinds(), Tags: nostr.TagMap{"a": {repo.String()}}, Since: since}
}
func TargetStatusFilter(repo RepositoryCoordinate, target nostr.ID, since nostr.Timestamp) nostr.Filter {
	return nostr.Filter{Kinds: statusKinds(), Tags: nostr.TagMap{"a": {repo.String()}, "e": {target.Hex()}}, Since: since}
}
func GraspListFilter(authors []nostr.PubKey, since nostr.Timestamp) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{KindGraspList}, Authors: authors, Since: since}
}

func collaborationTarget(event nostr.Event) (RepositoryCoordinate, nostr.PubKey, error) {
	switch event.Kind {
	case KindPatch:
		patch, err := ParsePatch(event)
		if err != nil {
			return RepositoryCoordinate{}, nostr.PubKey{}, err
		}
		if !patch.Root {
			return RepositoryCoordinate{}, nostr.PubKey{}, fmt.Errorf("status target patch must be a root patch")
		}
		return patch.Repository, event.PubKey, nil
	case KindPullRequest:
		pr, err := ParsePullRequest(event)
		if err != nil {
			return RepositoryCoordinate{}, nostr.PubKey{}, err
		}
		return pr.Repository, event.PubKey, nil
	case KindIssue:
		issue, err := ParseIssue(event)
		if err != nil {
			return RepositoryCoordinate{}, nostr.PubKey{}, err
		}
		return issue.Repository, event.PubKey, nil
	default:
		return RepositoryCoordinate{}, nostr.PubKey{}, fmt.Errorf("event kind %d cannot have NIP-34 status", event.Kind)
	}
}

func isStatusKind(kind nostr.Kind) bool { return kind >= KindStatusOpen && kind <= KindStatusDraft }
func statusKinds() []nostr.Kind {
	return []nostr.Kind{KindStatusOpen, KindStatusApplied, KindStatusClosed, KindStatusDraft}
}
func authorizedMaintainer(author nostr.PubKey, repo RepositoryAnnouncement) bool {
	return author == repo.Event.PubKey || containsPubKey(repo.Maintainers, author)
}
func authorizedStatusAuthor(author nostr.PubKey, repo RepositoryAnnouncement, targetAuthor nostr.PubKey) bool {
	return author == targetAuthor || authorizedMaintainer(author, repo)
}
func containsPubKey(values []nostr.PubKey, wanted nostr.PubKey) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
