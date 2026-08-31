package nip34

import (
	"context"
	"fmt"

	nostr "fiatjaf.com/nostr"
)

func BuildPatch(ctx context.Context, signer nostr.Signer, patch Patch) (nostr.Event, error) {
	if patch.Content == "" {
		return nostr.Event{}, fmt.Errorf("patch content required")
	}
	tags, err := collaborationTags(patch.Repository, patch.EarliestUniqueCommit, patch.Recipients)
	if err != nil {
		return nostr.Event{}, err
	}
	if patch.Root {
		tags = append(tags, nostr.Tag{"t", "root"})
	}
	if patch.RootRevision {
		if patch.Reply == nil {
			return nostr.Event{}, fmt.Errorf("root revision requires reply to original root")
		}
		tags = append(tags, nostr.Tag{"t", "root-revision"})
	}
	if patch.Reply != nil {
		tag := nostr.Tag{"e", patch.Reply.ID.Hex(), patch.Reply.RelayHint, "reply"}
		if patch.Reply.Author != nil {
			tag = append(tag, patch.Reply.Author.Hex())
		}
		tags = append(tags, tag)
	}
	commit := patch.Commit
	if commit.CommitID == "" && (commit.ParentCommitID != "" || commit.PGPSignature != "" || commit.CommitterName != "" || commit.CommitterEmail != "" || commit.Timestamp != "" || commit.TimezoneOffset != "") {
		return nostr.Event{}, fmt.Errorf("commit id required with stable commit metadata")
	}
	if commit.CommitID != "" {
		if err := validateGitID(commit.CommitID); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"commit", commit.CommitID}, nostr.Tag{"r", commit.CommitID})
		if commit.ParentCommitID == "" {
			return nostr.Event{}, fmt.Errorf("parent commit required with stable commit metadata")
		}
		if err := validateGitID(commit.ParentCommitID); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"parent-commit", commit.ParentCommitID}, nostr.Tag{"commit-pgp-sig", commit.PGPSignature})
		if commit.CommitterName == "" || commit.CommitterEmail == "" || commit.Timestamp == "" || commit.TimezoneOffset == "" {
			return nostr.Event{}, fmt.Errorf("complete committer metadata required")
		}
		tags = append(tags, nostr.Tag{"committer", commit.CommitterName, commit.CommitterEmail, commit.Timestamp, commit.TimezoneOffset})
	}
	return signEvent(ctx, signer, KindPatch, patch.Content, tags)
}

func ParsePatch(event nostr.Event) (Patch, error) {
	if err := validateSignedEvent(event, KindPatch); err != nil {
		return Patch{}, err
	}
	repo, euc, recipients, warnings := parseCollaborationTags(event.Tags)
	patch := Patch{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Content: event.Content, Warnings: warnings, Event: event}
	if patch.Content == "" {
		patch.Warnings = append(patch.Warnings, "patch content is recommended")
	}
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			switch tag[1] {
			case "root":
				if patch.Root {
					patch.Warnings = append(patch.Warnings, "duplicate root tag ignored")
					continue
				}
				patch.Root = true
			case "root-revision":
				if patch.RootRevision {
					patch.Warnings = append(patch.Warnings, "duplicate root-revision tag ignored")
					continue
				}
				patch.RootRevision = true
			}
		}
		if len(tag) >= 4 && len(tag) <= 5 && tag[0] == "e" && tag[3] == "reply" {
			if patch.Reply != nil {
				patch.Warnings = append(patch.Warnings, "duplicate reply tag ignored")
				continue
			}
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				patch.Warnings = append(patch.Warnings, "invalid reply event id ignored")
				continue
			}
			ref := &EventReference{ID: id, RelayHint: tag[2]}
			if len(tag) == 5 && tag[4] != "" {
				pk, err := parsePubKey(tag[4])
				if err != nil {
					patch.Warnings = append(patch.Warnings, "invalid reply author ignored")
				} else {
					ref.Author = &pk
				}
			}
			patch.Reply = ref
		}
	}
	if patch.RootRevision && patch.Reply == nil {
		patch.Warnings = append(patch.Warnings, "root-revision should reference the original root")
	}
	patch.Commit.CommitID = firstTagValue(event.Tags, "commit", &patch.Warnings)
	patch.Commit.ParentCommitID = firstTagValue(event.Tags, "parent-commit", &patch.Warnings)
	patch.Commit.PGPSignature = firstTagValue(event.Tags, "commit-pgp-sig", &patch.Warnings)
	committer := tagsNamed(event.Tags, "committer")
	if patch.Commit.CommitID != "" {
		if err := validateGitID(patch.Commit.CommitID); err != nil {
			patch.Warnings = append(patch.Warnings, "invalid commit id ignored")
			patch.Commit.CommitID = ""
		}
	}
	if patch.Commit.ParentCommitID != "" {
		if err := validateGitID(patch.Commit.ParentCommitID); err != nil {
			patch.Warnings = append(patch.Warnings, "invalid parent-commit ignored")
			patch.Commit.ParentCommitID = ""
		}
	}
	if len(committer) > 0 && len(committer[0]) == 5 {
		patch.Commit.CommitterName, patch.Commit.CommitterEmail, patch.Commit.Timestamp, patch.Commit.TimezoneOffset = committer[0][1], committer[0][2], committer[0][3], committer[0][4]
	} else if len(committer) > 0 {
		patch.Warnings = append(patch.Warnings, "malformed committer metadata ignored")
	}
	if patch.Commit.CommitID == "" && (patch.Commit.ParentCommitID != "" || patch.Commit.PGPSignature != "" || len(committer) > 0) {
		patch.Warnings = append(patch.Warnings, "stable commit metadata is incomplete")
	}
	patch.EarliestUniqueCommit = ""
	commitRefs := 0
	for _, tag := range event.Tags {
		if len(tag) != 2 || tag[0] != "r" {
			continue
		}
		if tag[1] == patch.Commit.CommitID {
			commitRefs++
			continue
		}
		if patch.EarliestUniqueCommit != "" {
			patch.Warnings = append(patch.Warnings, "additional earliest unique commit ignored")
			continue
		}
		if err := validateGitID(tag[1]); err != nil {
			patch.Warnings = append(patch.Warnings, "invalid earliest unique commit ignored")
			continue
		}
		patch.EarliestUniqueCommit = tag[1]
	}
	if patch.EarliestUniqueCommit == "" && patch.Commit.CommitID != "" && commitRefs > 1 {
		patch.EarliestUniqueCommit = patch.Commit.CommitID
	}
	return patch, nil
}

func BuildPullRequest(ctx context.Context, signer nostr.Signer, pr PullRequest) (nostr.Event, error) {
	if pr.Subject == "" || pr.Content == "" || pr.Tip == "" || len(pr.Clone) == 0 {
		return nostr.Event{}, fmt.Errorf("pull request subject, content, tip and clone URL required")
	}
	if err := validateGitID(pr.Tip); err != nil {
		return nostr.Event{}, err
	}
	tags, err := collaborationTags(pr.Repository, pr.EarliestUniqueCommit, pr.Recipients)
	if err != nil {
		return nostr.Event{}, err
	}
	for _, clone := range pr.Clone {
		if clone == "" {
			return nostr.Event{}, fmt.Errorf("empty PR clone URL")
		}
	}
	tags = append(tags, nostr.Tag{"subject", pr.Subject}, nostr.Tag{"c", pr.Tip}, append(nostr.Tag{"clone"}, pr.Clone...))
	for _, label := range pr.Labels {
		if label == "" {
			return nostr.Event{}, fmt.Errorf("empty PR label")
		}
		tags = append(tags, nostr.Tag{"t", label})
	}
	if pr.BranchName != "" {
		tags = append(tags, nostr.Tag{"branch-name", pr.BranchName})
	}
	if pr.RevisionOf != nil {
		tags = append(tags, nostr.Tag{"e", pr.RevisionOf.Hex()})
	}
	if pr.MergeBase != "" {
		if err := validateGitID(pr.MergeBase); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"merge-base", pr.MergeBase})
	}
	return signEvent(ctx, signer, KindPullRequest, pr.Content, tags)
}

func ParsePullRequest(event nostr.Event) (PullRequest, error) {
	if err := validateSignedEvent(event, KindPullRequest); err != nil {
		return PullRequest{}, err
	}
	repo, euc, recipients, warnings := parseCollaborationTags(event.Tags)
	pr := PullRequest{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Content: event.Content, Warnings: warnings, Event: event}
	pr.Subject = firstTagValue(event.Tags, "subject", &pr.Warnings)
	if pr.Subject == "" {
		pr.Warnings = append(pr.Warnings, "subject tag is recommended")
	}
	pr.Tip = firstTagValue(event.Tags, "c", &pr.Warnings)
	if pr.Tip == "" {
		pr.Warnings = append(pr.Warnings, "tip commit tag is recommended")
	} else if err := validateGitID(pr.Tip); err != nil {
		pr.Warnings = append(pr.Warnings, "invalid tip commit ignored")
		pr.Tip = ""
	}
	pr.Clone = flattenedValues(event.Tags, "clone")
	validClones := pr.Clone[:0]
	for _, clone := range pr.Clone {
		if clone == "" {
			pr.Warnings = append(pr.Warnings, "empty clone URL ignored")
			continue
		}
		validClones = append(validClones, clone)
	}
	pr.Clone = validClones
	if event.Content == "" {
		pr.Warnings = append(pr.Warnings, "pull request content is recommended")
	}
	if len(pr.Clone) == 0 {
		pr.Warnings = append(pr.Warnings, "at least one clone URL is recommended")
	}
	pr.BranchName = firstTagValue(event.Tags, "branch-name", &pr.Warnings)
	pr.MergeBase = firstTagValue(event.Tags, "merge-base", &pr.Warnings)
	if pr.MergeBase != "" {
		if err := validateGitID(pr.MergeBase); err != nil {
			pr.Warnings = append(pr.Warnings, "invalid merge-base ignored")
			pr.MergeBase = ""
		}
	}
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			pr.Labels = append(pr.Labels, tag[1])
		}
		if len(tag) == 2 && tag[0] == "e" {
			if pr.RevisionOf != nil {
				pr.Warnings = append(pr.Warnings, "additional revision event ignored")
				continue
			}
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				pr.Warnings = append(pr.Warnings, "invalid revision event ignored")
				continue
			}
			pr.RevisionOf = &id
		}
	}
	return pr, nil
}

func BuildPullRequestUpdate(ctx context.Context, signer nostr.Signer, update PullRequestUpdate) (nostr.Event, error) {
	if update.Tip == "" || len(update.Clone) == 0 || update.PullRequestID == (nostr.ID{}) || update.PullRequestAuthor == (nostr.PubKey{}) {
		return nostr.Event{}, fmt.Errorf("PR update root, author, tip and clone URL required")
	}
	if err := validateGitID(update.Tip); err != nil {
		return nostr.Event{}, err
	}
	tags, err := collaborationTags(update.Repository, update.EarliestUniqueCommit, update.Recipients)
	if err != nil {
		return nostr.Event{}, err
	}
	for _, clone := range update.Clone {
		if clone == "" {
			return nostr.Event{}, fmt.Errorf("empty PR update clone URL")
		}
	}
	tags = append(tags, nostr.Tag{"E", update.PullRequestID.Hex()}, nostr.Tag{"P", update.PullRequestAuthor.Hex()}, nostr.Tag{"c", update.Tip}, append(nostr.Tag{"clone"}, update.Clone...))
	if update.MergeBase != "" {
		if err := validateGitID(update.MergeBase); err != nil {
			return nostr.Event{}, err
		}
		tags = append(tags, nostr.Tag{"merge-base", update.MergeBase})
	}
	return signEvent(ctx, signer, KindPullRequestUpdate, "", tags)
}

func ParsePullRequestUpdate(event nostr.Event) (PullRequestUpdate, error) {
	if err := validateSignedEvent(event, KindPullRequestUpdate); err != nil {
		return PullRequestUpdate{}, err
	}
	repo, euc, recipients, warnings := parseCollaborationTags(event.Tags)
	update := PullRequestUpdate{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Warnings: warnings, Event: event}
	root := firstTagValue(event.Tags, "E", &update.Warnings)
	if root == "" {
		update.Warnings = append(update.Warnings, "pull request root event is recommended")
	} else if id, err := nostr.IDFromHex(root); err != nil {
		update.Warnings = append(update.Warnings, "invalid pull request root event ignored")
	} else {
		update.PullRequestID = id
	}
	author := firstTagValue(event.Tags, "P", &update.Warnings)
	if author == "" {
		update.Warnings = append(update.Warnings, "pull request author is recommended")
	} else if pk, err := parsePubKey(author); err != nil {
		update.Warnings = append(update.Warnings, "invalid pull request author ignored")
	} else {
		update.PullRequestAuthor = pk
	}
	update.Tip = firstTagValue(event.Tags, "c", &update.Warnings)
	if update.Tip == "" {
		update.Warnings = append(update.Warnings, "tip commit tag is recommended")
	} else if err := validateGitID(update.Tip); err != nil {
		update.Warnings = append(update.Warnings, "invalid tip commit ignored")
		update.Tip = ""
	}
	update.Clone = flattenedValues(event.Tags, "clone")
	validClones := update.Clone[:0]
	for _, clone := range update.Clone {
		if clone == "" {
			update.Warnings = append(update.Warnings, "empty clone URL ignored")
			continue
		}
		validClones = append(validClones, clone)
	}
	update.Clone = validClones
	if len(update.Clone) == 0 {
		update.Warnings = append(update.Warnings, "at least one clone URL is recommended")
	}
	update.MergeBase = firstTagValue(event.Tags, "merge-base", &update.Warnings)
	if update.MergeBase != "" {
		if err := validateGitID(update.MergeBase); err != nil {
			update.Warnings = append(update.Warnings, "invalid merge-base ignored")
			update.MergeBase = ""
		}
	}
	return update, nil
}

func BuildIssue(ctx context.Context, signer nostr.Signer, issue Issue) (nostr.Event, error) {
	if issue.Content == "" {
		return nostr.Event{}, fmt.Errorf("issue content required")
	}
	tags, err := collaborationTags(issue.Repository, "", issue.Recipients)
	if err != nil {
		return nostr.Event{}, err
	}
	if issue.Subject != "" {
		tags = append(tags, nostr.Tag{"subject", issue.Subject})
	}
	for _, label := range issue.Labels {
		if label == "" {
			return nostr.Event{}, fmt.Errorf("empty issue label")
		}
		tags = append(tags, nostr.Tag{"t", label})
	}
	return signEvent(ctx, signer, KindIssue, issue.Content, tags)
}

func ParseIssue(event nostr.Event) (Issue, error) {
	if err := validateSignedEvent(event, KindIssue); err != nil {
		return Issue{}, err
	}
	repo, _, recipients, warnings := parseCollaborationTags(event.Tags)
	issue := Issue{Repository: repo, Recipients: recipients, Content: event.Content, Warnings: warnings, Event: event}
	if issue.Content == "" {
		issue.Warnings = append(issue.Warnings, "issue content is recommended")
	}
	issue.Subject = firstTagValue(event.Tags, "subject", &issue.Warnings)
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			if tag[1] == "" {
				issue.Warnings = append(issue.Warnings, "empty issue label ignored")
				continue
			}
			issue.Labels = append(issue.Labels, tag[1])
		}
	}
	return issue, nil
}

func collaborationTags(repo RepositoryCoordinate, euc string, recipients []nostr.PubKey) (nostr.Tags, error) {
	if _, err := ParseRepositoryCoordinate(repo.String()); err != nil {
		return nil, err
	}
	tags := nostr.Tags{{"a", repo.String()}}
	if euc != "" {
		if err := validateGitID(euc); err != nil {
			return nil, err
		}
		tags = append(tags, nostr.Tag{"r", euc})
	}
	all := append([]nostr.PubKey{repo.Owner}, recipients...)
	for _, recipient := range uniquePubKeys(all) {
		tags = append(tags, nostr.Tag{"p", recipient.Hex()})
	}
	return tags, nil
}

func parseCollaborationTags(tags nostr.Tags) (RepositoryCoordinate, string, []nostr.PubKey, []string) {
	warnings := []string{}
	raw := firstTagValue(tags, "a", &warnings)
	repo := RepositoryCoordinate{}
	if raw == "" {
		warnings = append(warnings, "repository coordinate is recommended")
	} else if parsed, err := ParseRepositoryCoordinate(raw); err != nil {
		warnings = append(warnings, "invalid repository coordinate ignored")
	} else {
		repo = parsed
	}
	recipients := []nostr.PubKey{}
	for _, tag := range tags {
		if len(tag) > 0 && tag[0] == "p" {
			if len(tag) != 2 {
				warnings = append(warnings, "malformed recipient tag ignored")
				continue
			}
			pk, err := parsePubKey(tag[1])
			if err != nil {
				warnings = append(warnings, "invalid recipient pubkey ignored")
				continue
			}
			recipients = append(recipients, pk)
		}
	}
	if repo.Owner != (nostr.PubKey{}) {
		foundOwner := false
		for _, pk := range recipients {
			if pk == repo.Owner {
				foundOwner = true
			}
		}
		if !foundOwner {
			warnings = append(warnings, "repository owner recipient tag is recommended")
		}
	}
	euc := ""
	for _, tag := range tags {
		if len(tag) == 2 && tag[0] == "r" {
			if euc != "" {
				warnings = append(warnings, "additional earliest unique commit ignored")
				continue
			}
			if err := validateGitID(tag[1]); err != nil {
				warnings = append(warnings, "invalid earliest unique commit ignored")
				continue
			}
			euc = tag[1]
		}
	}
	return repo, euc, uniquePubKeys(recipients), warnings
}

// firstTagValue returns the first well-shaped value and records duplicates or
// malformed tags as advisory warnings instead of rejecting a signed event.
func firstTagValue(tags nostr.Tags, name string, warnings *[]string) string {
	value := ""
	found := false
	for _, tag := range tags {
		if len(tag) == 0 || tag[0] != name {
			continue
		}
		if len(tag) != 2 || tag[1] == "" {
			*warnings = append(*warnings, fmt.Sprintf("malformed %s tag ignored", name))
			continue
		}
		if found {
			*warnings = append(*warnings, fmt.Sprintf("additional %s tag ignored", name))
			continue
		}
		value, found = tag[1], true
	}
	return value
}

func tagsNamed(tags nostr.Tags, name string) []nostr.Tag {
	out := []nostr.Tag{}
	for _, tag := range tags {
		if len(tag) > 0 && tag[0] == name {
			out = append(out, tag)
		}
	}
	return out
}
func flattenedValues(tags nostr.Tags, name string) []string {
	out := []string{}
	for _, tag := range tags {
		if len(tag) > 1 && tag[0] == name {
			out = append(out, tag[1:]...)
		}
	}
	return out
}
