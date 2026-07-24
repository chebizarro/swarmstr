package channels

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPairingStorePersistsObservedRequestsAndApprovesAfterCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairing.json")
	store, err := NewPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	observed, err := store.UpsertObserved("telegram", "work", "sender-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if observed.SenderID != "sender-1" || observed.RequestID == "" {
		t.Fatalf("observed = %#v", observed)
	}

	reopened, err := NewPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := reopened.List("telegram", "work", now)
	if err != nil || len(listed) != 1 || listed[0].RequestID != observed.RequestID {
		t.Fatalf("reopened list = %#v, %v", listed, err)
	}

	commitErr := errors.New("config write failed")
	if _, err := reopened.Approve(observed.RequestID, func(PairingRequest) error { return commitErr }); !errors.Is(err, commitErr) {
		t.Fatalf("approve error = %v, want %v", err, commitErr)
	}
	if _, ok := reopened.Get(observed.RequestID); !ok {
		t.Fatal("failed durable commit removed pending request")
	}

	committed := false
	approved, err := reopened.Approve(observed.RequestID, func(req PairingRequest) error {
		committed = req.SenderID == "sender-1"
		return nil
	})
	if err != nil || !committed || approved.RequestID != observed.RequestID {
		t.Fatalf("approve = %#v, committed=%v, err=%v", approved, committed, err)
	}
	if _, ok := reopened.Get(observed.RequestID); ok {
		t.Fatal("approved request remains pending")
	}
	if resurrected, created, err := reopened.UpsertObservedAt("telegram", "work", "sender-1", now, now.Add(time.Hour)); err != nil || created || resurrected.RequestID != "" {
		t.Fatalf("older observation resurrected approval: %#v created=%v err=%v", resurrected, created, err)
	}

	finalStore, err := NewPairingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	final, err := finalStore.List("", "", now)
	if err != nil || len(final) != 0 {
		t.Fatalf("final list = %#v, %v", final, err)
	}
}

func TestPairingStoreRefreshesExistingSenderWithoutMessageContent(t *testing.T) {
	store, err := NewPairingStore("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, err := store.UpsertObserved("nextcloud", "main", "sender", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.UpsertObserved("nextcloud-talk", "main", "sender", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestID != second.RequestID || first.CreatedAtMS != second.CreatedAtMS || second.LastSeenMS <= first.LastSeenMS {
		t.Fatalf("refresh first=%#v second=%#v", first, second)
	}
}
