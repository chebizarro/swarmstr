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
	repo, euc, recipients, err := parseCollaborationTags(event.Tags)
	if err != nil {
		return Patch{}, err
	}
	patch := Patch{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Content: event.Content, Event: event}
	if patch.Content == "" {
		return Patch{}, fmt.Errorf("patch content required")
	}
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			switch tag[1] {
			case "root":
				if patch.Root {
					return Patch{}, fmt.Errorf("duplicate root tag")
				}
				patch.Root = true
			case "root-revision":
				if patch.RootRevision {
					return Patch{}, fmt.Errorf("duplicate root revision tag")
				}
				patch.RootRevision = true
			}
		}
		if len(tag) >= 4 && len(tag) <= 5 && tag[0] == "e" && tag[3] == "reply" {
			if patch.Reply != nil {
				return Patch{}, fmt.Errorf("duplicate reply tag")
			}
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				return Patch{}, fmt.Errorf("invalid reply event id")
			}
			ref := &EventReference{ID: id, RelayHint: tag[2]}
			if len(tag) == 5 && tag[4] != "" {
				pk, err := parsePubKey(tag[4])
				if err != nil {
					return Patch{}, err
				}
				ref.Author = &pk
			}
			patch.Reply = ref
		}
	}
	if patch.RootRevision && patch.Reply == nil {
		return Patch{}, fmt.Errorf("root revision requires reply")
	}
	if patch.Commit.CommitID, err = optionalSingleton(event.Tags, "commit"); err != nil {
		return Patch{}, err
	}
	if patch.Commit.ParentCommitID, err = optionalSingleton(event.Tags, "parent-commit"); err != nil {
		return Patch{}, err
	}
	if patch.Commit.PGPSignature, err = optionalSingleton(event.Tags, "commit-pgp-sig"); err != nil {
		return Patch{}, err
	}
	committer := tagsNamed(event.Tags, "committer")
	pgpTags := tagsNamed(event.Tags, "commit-pgp-sig")
	if patch.Commit.CommitID == "" && (patch.Commit.ParentCommitID != "" || len(pgpTags) > 0 || len(committer) > 0) {
		return Patch{}, fmt.Errorf("partial stable commit metadata")
	}
	if patch.Commit.CommitID != "" {
		if err := validateGitID(patch.Commit.CommitID); err != nil {
			return Patch{}, err
		}
		if err := validateGitID(patch.Commit.ParentCommitID); err != nil {
			return Patch{}, err
		}
		if len(pgpTags) != 1 || len(pgpTags[0]) != 2 || len(committer) != 1 || len(committer[0]) != 5 {
			return Patch{}, fmt.Errorf("incomplete stable commit metadata")
		}
		patch.Commit.CommitterName, patch.Commit.CommitterEmail, patch.Commit.Timestamp, patch.Commit.TimezoneOffset = committer[0][1], committer[0][2], committer[0][3], committer[0][4]
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
			return Patch{}, fmt.Errorf("duplicate earliest unique commit")
		}
		if err := validateGitID(tag[1]); err != nil {
			return Patch{}, err
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
	repo, euc, recipients, err := parseCollaborationTags(event.Tags)
	if err != nil {
		return PullRequest{}, err
	}
	pr := PullRequest{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Content: event.Content, Event: event}
	if pr.Subject, err = requiredSingleton(event.Tags, "subject"); err != nil {
		return PullRequest{}, err
	}
	if pr.Tip, err = requiredSingleton(event.Tags, "c"); err != nil {
		return PullRequest{}, err
	}
	if err := validateGitID(pr.Tip); err != nil {
		return PullRequest{}, err
	}
	pr.Clone = flattenedValues(event.Tags, "clone")
	for _, clone := range pr.Clone {
		if clone == "" {
			return PullRequest{}, fmt.Errorf("empty PR clone URL")
		}
	}
	if event.Content == "" || len(pr.Clone) == 0 {
		return PullRequest{}, fmt.Errorf("pull request content and clone URL required")
	}
	if pr.BranchName, err = optionalSingleton(event.Tags, "branch-name"); err != nil {
		return PullRequest{}, err
	}
	if pr.MergeBase, err = optionalSingleton(event.Tags, "merge-base"); err != nil {
		return PullRequest{}, err
	}
	if pr.MergeBase != "" {
		if err := validateGitID(pr.MergeBase); err != nil {
			return PullRequest{}, err
		}
	}
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			pr.Labels = append(pr.Labels, tag[1])
		}
		if len(tag) == 2 && tag[0] == "e" {
			if pr.RevisionOf != nil {
				return PullRequest{}, fmt.Errorf("duplicate revision event")
			}
			id, err := nostr.IDFromHex(tag[1])
			if err != nil {
				return PullRequest{}, err
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
	repo, euc, recipients, err := parseCollaborationTags(event.Tags)
	if err != nil {
		return PullRequestUpdate{}, err
	}
	update := PullRequestUpdate{Repository: repo, EarliestUniqueCommit: euc, Recipients: recipients, Event: event}
	root, err := requiredSingleton(event.Tags, "E")
	if err != nil {
		return PullRequestUpdate{}, err
	}
	update.PullRequestID, err = nostr.IDFromHex(root)
	if err != nil {
		return PullRequestUpdate{}, err
	}
	author, err := requiredSingleton(event.Tags, "P")
	if err != nil {
		return PullRequestUpdate{}, err
	}
	update.PullRequestAuthor, err = parsePubKey(author)
	if err != nil {
		return PullRequestUpdate{}, err
	}
	if update.Tip, err = requiredSingleton(event.Tags, "c"); err != nil {
		return PullRequestUpdate{}, err
	}
	if err := validateGitID(update.Tip); err != nil {
		return PullRequestUpdate{}, err
	}
	update.Clone = flattenedValues(event.Tags, "clone")
	for _, clone := range update.Clone {
		if clone == "" {
			return PullRequestUpdate{}, fmt.Errorf("empty PR update clone URL")
		}
	}
	if len(update.Clone) == 0 {
		return PullRequestUpdate{}, fmt.Errorf("PR update clone URL required")
	}
	if update.MergeBase, err = optionalSingleton(event.Tags, "merge-base"); err != nil {
		return PullRequestUpdate{}, err
	}
	if update.MergeBase != "" {
		if err := validateGitID(update.MergeBase); err != nil {
			return PullRequestUpdate{}, err
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
	repo, _, recipients, err := parseCollaborationTags(event.Tags)
	if err != nil {
		return Issue{}, err
	}
	issue := Issue{Repository: repo, Recipients: recipients, Content: event.Content, Event: event}
	if issue.Content == "" {
		return Issue{}, fmt.Errorf("issue content required")
	}
	if issue.Subject, err = optionalSingleton(event.Tags, "subject"); err != nil {
		return Issue{}, err
	}
	for _, tag := range event.Tags {
		if len(tag) == 2 && tag[0] == "t" {
			if tag[1] == "" {
				return Issue{}, fmt.Errorf("empty issue label")
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

func parseCollaborationTags(tags nostr.Tags) (RepositoryCoordinate, string, []nostr.PubKey, error) {
	raw, err := requiredSingleton(tags, "a")
	if err != nil {
		return RepositoryCoordinate{}, "", nil, err
	}
	repo, err := ParseRepositoryCoordinate(raw)
	if err != nil {
		return RepositoryCoordinate{}, "", nil, err
	}
	recipients := []nostr.PubKey{}
	for _, tag := range tags {
		if len(tag) > 0 && tag[0] == "p" {
			if len(tag) != 2 {
				return RepositoryCoordinate{}, "", nil, fmt.Errorf("invalid p tag")
			}
			pk, err := parsePubKey(tag[1])
			if err != nil {
				return RepositoryCoordinate{}, "", nil, err
			}
			recipients = append(recipients, pk)
		}
	}
	foundOwner := false
	for _, pk := range recipients {
		if pk == repo.Owner {
			foundOwner = true
		}
	}
	if !foundOwner {
		return RepositoryCoordinate{}, "", nil, fmt.Errorf("repository owner p tag required")
	}
	euc := ""
	for _, tag := range tags {
		if len(tag) == 2 && tag[0] == "r" {
			if euc == "" {
				if err := validateGitID(tag[1]); err != nil {
					return RepositoryCoordinate{}, "", nil, err
				}
				euc = tag[1]
			}
		}
	}
	return repo, euc, uniquePubKeys(recipients), nil
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
