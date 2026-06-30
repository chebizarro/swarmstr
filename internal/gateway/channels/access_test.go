package channels

import "testing"

func TestDecideAccess_AllowsSender(t *testing.T) {
	decision := DecideAccess(AccessMessage{SenderID: "user-1"}, AccessPolicy{AllowFrom: []string{"user-1"}})
	if !decision.Allowed || decision.Reason != "allowed" {
		t.Fatalf("expected allowed, got %+v", decision)
	}
}

func TestDecideAccess_DeniesSender(t *testing.T) {
	decision := DecideAccess(AccessMessage{SenderID: "user-2"}, AccessPolicy{AllowFrom: []string{"user-1"}})
	if decision.Allowed || decision.Reason != "sender_denied" {
		t.Fatalf("expected sender denial, got %+v", decision)
	}
}

func TestDecideAccess_MentionGated(t *testing.T) {
	decision := DecideAccess(
		AccessMessage{SenderID: "user-1", IsGroup: true, CanDetectMention: true},
		AccessPolicy{AllowFrom: []string{"user-1"}, RequireMention: true},
	)
	if decision.Allowed || decision.Reason != "mention_required" || decision.MentionAllowed {
		t.Fatalf("expected mention gate denial, got %+v", decision)
	}
}

func TestDecideAccess_DMOpenAllowsEmptyAllowlist(t *testing.T) {
	decision := DecideAccess(
		AccessMessage{SenderID: "new-user", IsDM: true},
		AccessPolicy{DMPolicy: DMAccessPolicyOpen},
	)
	if !decision.Allowed {
		t.Fatalf("expected open DM allowed, got %+v", decision)
	}
}

func TestDecideAccess_RuntimeAccessGroup(t *testing.T) {
	decision := DecideAccess(
		AccessMessage{SenderID: "member-1"},
		AccessPolicy{
			AllowFrom:    []string{"accessGroup:trusted"},
			AccessGroups: map[string][]string{"trusted": {"member-1"}},
		},
	)
	if !decision.Allowed {
		t.Fatalf("expected access group member allowed, got %+v", decision)
	}
}

func TestResolveEffectiveAllowFrom(t *testing.T) {
	falseValue := false
	dm, group := ResolveEffectiveAllowFrom(AccessPolicy{
		AllowFrom:                         []string{" a ", "A", "b"},
		StoreAllowFrom:                    []string{"stored"},
		GroupAllowFromFallbackToAllowFrom: &falseValue,
	})
	if len(dm) != 3 || dm[0] != "a" || dm[1] != "b" || dm[2] != "stored" {
		t.Fatalf("unexpected dm allow list: %#v", dm)
	}
	if len(group) != 0 {
		t.Fatalf("expected no group fallback, got %#v", group)
	}
}
