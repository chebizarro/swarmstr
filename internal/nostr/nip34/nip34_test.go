package nip34

import (
	"context"
	"reflect"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/keyer"
)

func TestRepositoryAnnouncementAndStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	owner := keyer.NewPlainKeySigner(nostr.Generate())
	maintainer := keyer.NewPlainKeySigner(nostr.Generate())
	maintainerPK, _ := maintainer.GetPublicKey(ctx)
	repoEvent, err := BuildRepositoryAnnouncement(ctx, owner, RepositoryAnnouncement{
		Identifier: "swarm:str", Name: "Swarmstr", Description: "agents on nostr",
		Web: []string{"https://example.com/swarmstr"}, Clone: []string{"git@example.com:swarmstr.git"},
		Relays: []string{"wss://relay.example"}, EarliestUniqueCommit: gitID('a'), Maintainers: []nostr.PubKey{maintainerPK}, Topics: []string{"agents", "nostr"},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := ParseRepositoryAnnouncement(repoEvent)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Identifier != "swarm:str" || repo.Name != "Swarmstr" || repo.EarliestUniqueCommit != gitID('a') || len(repo.Maintainers) != 1 || repo.Coordinate().String() != "30617:"+repoEvent.PubKey.Hex()+":swarm:str" {
		t.Fatalf("repo = %#v", repo)
	}
	coordinate, err := ParseRepositoryCoordinate(repo.Coordinate().String())
	if err != nil || coordinate != repo.Coordinate() {
		t.Fatalf("coordinate = %#v %v", coordinate, err)
	}

	stateEvent, err := BuildRepositoryState(ctx, owner, RepositoryState{Identifier: repo.Identifier, HEAD: "ref: refs/heads/main", Refs: map[string]string{"refs/tags/v1": gitID('b'), "refs/heads/main": gitID('c')}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := ParseRepositoryState(stateEvent)
	if err != nil {
		t.Fatal(err)
	}
	if state.HEAD != "ref: refs/heads/main" || state.Refs["refs/heads/main"] != gitID('c') {
		t.Fatalf("state = %#v", state)
	}

	tampered := repoEvent
	tampered.Content = "tampered"
	if _, err := ParseRepositoryAnnouncement(tampered); err == nil {
		t.Fatal("tampered announcement accepted")
	}
}

func TestPatchPullRequestIssueAndUpdateRoundTrip(t *testing.T) {
	ctx := context.Background()
	owner := keyer.NewPlainKeySigner(nostr.Generate())
	author := keyer.NewPlainKeySigner(nostr.Generate())
	ownerPK, _ := owner.GetPublicKey(ctx)
	authorPK, _ := author.GetPublicKey(ctx)
	repo := RepositoryCoordinate{Owner: ownerPK, Identifier: "repo"}
	rootID := testID('r')
	patchEvent, err := BuildPatch(ctx, author, Patch{Repository: repo, EarliestUniqueCommit: gitID('a'), Root: true, RootRevision: true, Reply: &EventReference{ID: rootID}, Content: "From abc Mon Sep 17 00:00:00 2001\n", Commit: CommitMetadata{CommitID: gitID('b'), ParentCommitID: gitID('c'), PGPSignature: "", CommitterName: "Agent", CommitterEmail: "agent@example.com", Timestamp: "1", TimezoneOffset: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := ParsePatch(patchEvent)
	if err != nil {
		t.Fatal(err)
	}
	if !patch.Root || !patch.RootRevision || patch.Reply == nil || patch.Reply.ID != rootID || patch.Commit.CommitID != gitID('b') || !containsPubKey(patch.Recipients, ownerPK) {
		t.Fatalf("patch = %#v", patch)
	}

	prEvent, err := BuildPullRequest(ctx, author, PullRequest{Repository: repo, EarliestUniqueCommit: gitID('a'), Subject: "Add sync", Labels: []string{"feature"}, Tip: gitID('d'), Clone: []string{"https://git.example/repo.git"}, BranchName: "sync", MergeBase: gitID('e'), Content: "Please merge"})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := ParsePullRequest(prEvent)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Subject != "Add sync" || pr.Tip != gitID('d') || !reflect.DeepEqual(pr.Labels, []string{"feature"}) {
		t.Fatalf("pr = %#v", pr)
	}

	updateEvent, err := BuildPullRequestUpdate(ctx, author, PullRequestUpdate{Repository: repo, EarliestUniqueCommit: gitID('a'), PullRequestID: prEvent.ID, PullRequestAuthor: authorPK, Tip: gitID('f'), Clone: []string{"https://git.example/repo.git"}, MergeBase: gitID('e')})
	if err != nil {
		t.Fatal(err)
	}
	update, err := ParsePullRequestUpdate(updateEvent)
	if err != nil {
		t.Fatal(err)
	}
	if update.PullRequestID != prEvent.ID || update.PullRequestAuthor != authorPK || update.Tip != gitID('f') {
		t.Fatalf("update = %#v", update)
	}

	issueEvent, err := BuildIssue(ctx, author, Issue{Repository: repo, Subject: "Bug", Labels: []string{"bug", "agent"}, Content: "Something failed"})
	if err != nil {
		t.Fatal(err)
	}
	issue, err := ParseIssue(issueEvent)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Subject != "Bug" || len(issue.Labels) != 2 || !containsPubKey(issue.Recipients, ownerPK) {
		t.Fatalf("issue = %#v", issue)
	}
}

func TestStatusAuthorizationAndLatestSelection(t *testing.T) {
	ctx := context.Background()
	owner := keyer.NewPlainKeySigner(nostr.Generate())
	maintainer := keyer.NewPlainKeySigner(nostr.Generate())
	outsider := keyer.NewPlainKeySigner(nostr.Generate())
	author := keyer.NewPlainKeySigner(nostr.Generate())
	ownerPK, _ := owner.GetPublicKey(ctx)
	maintainerPK, _ := maintainer.GetPublicKey(ctx)
	repoEvent, _ := BuildRepositoryAnnouncement(ctx, owner, RepositoryAnnouncement{Identifier: "repo", Maintainers: []nostr.PubKey{maintainerPK}})
	repo, _ := ParseRepositoryAnnouncement(repoEvent)
	issueEvent, _ := BuildIssue(ctx, author, Issue{Repository: RepositoryCoordinate{Owner: ownerPK, Identifier: "repo"}, Content: "Issue"})
	if _, err := BuildStatus(ctx, outsider, repo, issueEvent, Status{Kind: KindStatusClosed}); err == nil {
		t.Fatal("unauthorized status signer accepted")
	}
	openEvent, err := BuildStatus(ctx, owner, repo, issueEvent, Status{Kind: KindStatusOpen, Content: "triaged"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildStatus(ctx, author, repo, issueEvent, Status{Kind: KindStatusOpen, Content: "author update"}); err != nil {
		t.Fatalf("target author status rejected: %v", err)
	}
	closedEvent, err := BuildStatus(ctx, maintainer, repo, issueEvent, Status{Kind: KindStatusClosed, Content: "fixed"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := ParseStatus(openEvent)
	if err != nil || open.Root != issueEvent.ID || open.Repository == nil {
		t.Fatalf("open = %#v %v", open, err)
	}
	latest, ok := LatestValidStatus(repo, issueEvent, []nostr.Event{openEvent, closedEvent})
	if !ok {
		t.Fatal("no latest status")
	}
	want := openEvent
	if closedEvent.CreatedAt > openEvent.CreatedAt || (closedEvent.CreatedAt == openEvent.CreatedAt && closedEvent.ID.Hex() > openEvent.ID.Hex()) {
		want = closedEvent
	}
	if latest.Event.ID != want.ID {
		t.Fatalf("latest = %s, want %s", latest.Event.ID.Hex(), want.ID.Hex())
	}
	if got := CurrentStatus(repo, issueEvent, nil); got != KindStatusOpen {
		t.Fatalf("default status = %d", got)
	}
	wrongTarget := closedEvent
	wrongTarget.Tags[0][1] = strings.Repeat("0", 64)
	_ = maintainer.SignEvent(ctx, &wrongTarget)
	if got, ok := LatestValidStatus(repo, issueEvent, []nostr.Event{wrongTarget}); ok || got.Event.ID != (nostr.ID{}) {
		t.Fatal("wrong-target status accepted")
	}
	accepted, other := testID('a'), testID('b')
	if got := EffectivePatchRevisionStatus(Status{Kind: KindStatusApplied, AcceptedRevision: &accepted}, accepted); got != KindStatusApplied {
		t.Fatalf("accepted revision status = %d", got)
	}
	if got := EffectivePatchRevisionStatus(Status{Kind: KindStatusApplied, AcceptedRevision: &accepted}, other); got != KindStatusClosed {
		t.Fatalf("unaccepted revision status = %d", got)
	}
}

func TestGraspListAndRoutingFilters(t *testing.T) {
	ctx := context.Background()
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	pk, _ := signer.GetPublicKey(ctx)
	event, err := BuildGraspList(ctx, signer, []string{"wss://grasp.example"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := ParseGraspList(event)
	if err != nil || !reflect.DeepEqual(list.Servers, []string{"wss://grasp.example"}) {
		t.Fatalf("list = %#v %v", list, err)
	}
	if latest, ok := LatestGraspList([]nostr.Event{event}, pk); !ok || latest.Event.ID != event.ID {
		t.Fatal("latest grasp list not selected")
	}
	repo := RepositoryCoordinate{Owner: pk, Identifier: "repo"}
	target := testID('x')
	activity := RepositoryActivityFilter(repo, 10)
	if len(activity.Kinds) != 4 || activity.Tags["a"][0] != repo.String() || activity.Since != 10 {
		t.Fatalf("activity filter = %#v", activity)
	}
	status := TargetStatusFilter(repo, target, 20)
	if len(status.Kinds) != 4 || status.Tags["e"][0] != target.Hex() || status.Since != 20 {
		t.Fatalf("status filter = %#v", status)
	}
	announcement := RepositoryAnnouncementFilter(pk, "repo")
	if announcement.Tags["d"][0] != "repo" || announcement.Authors[0] != pk {
		t.Fatalf("announcement filter = %#v", announcement)
	}
	if got := RelayTargets(RepositoryAnnouncement{Relays: []string{"wss://one.example", "wss://one.example", "bad"}}); !reflect.DeepEqual(got, []string{"wss://one.example"}) {
		t.Fatalf("relay targets = %v", got)
	}
}

func TestNIP34ValidationRejectsMalformedInputs(t *testing.T) {
	ctx := context.Background()
	signer := keyer.NewPlainKeySigner(nostr.Generate())
	pk, _ := signer.GetPublicKey(ctx)
	repo := RepositoryCoordinate{Owner: pk, Identifier: "repo"}
	if _, err := BuildRepositoryAnnouncement(ctx, signer, RepositoryAnnouncement{}); err == nil {
		t.Fatal("empty repo identifier accepted")
	}
	if _, err := BuildRepositoryState(ctx, signer, RepositoryState{Identifier: "repo", Refs: map[string]string{"main": gitID('a')}}); err == nil {
		t.Fatal("invalid ref accepted")
	}
	if _, err := BuildPatch(ctx, signer, Patch{Repository: repo, RootRevision: true, Content: "patch"}); err == nil {
		t.Fatal("root revision without reply accepted")
	}
	if _, err := BuildPullRequest(ctx, signer, PullRequest{Repository: repo}); err == nil {
		t.Fatal("empty PR accepted")
	}
	if _, err := BuildIssue(ctx, signer, Issue{Repository: repo}); err == nil {
		t.Fatal("empty issue accepted")
	}
	if _, err := BuildGraspList(ctx, signer, []string{"https://not-websocket.example"}); err == nil {
		t.Fatal("invalid grasp URL accepted")
	}
}

func gitID(char byte) string { return strings.Repeat(string(char), 40) }
func testID(char byte) nostr.ID {
	var id nostr.ID
	for i := range id {
		id[i] = char
	}
	return id
}
