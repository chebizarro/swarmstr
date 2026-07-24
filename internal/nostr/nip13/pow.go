package nip13

import (
	"context"
	"fmt"
	"math/bits"
	"strconv"

	nostr "fiatjaf.com/nostr"
)

func Difficulty(id nostr.ID) int {
	total := 0
	for _, b := range id {
		total += bits.LeadingZeros8(b)
		if b != 0 {
			break
		}
	}
	return total
}

func Commitment(event nostr.Event) (target int, nonce uint64, present bool, err error) {
	for _, tag := range event.Tags {
		if len(tag) == 0 || tag[0] != "nonce" {
			continue
		}
		if present {
			return 0, 0, false, fmt.Errorf("multiple nonce tags")
		}
		if len(tag) < 2 {
			return 0, 0, false, fmt.Errorf("malformed nonce tag")
		}
		nonce, err = strconv.ParseUint(tag[1], 10, 64)
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid nonce")
		}
		if len(tag) >= 3 {
			target, err = strconv.Atoi(tag[2])
			if err != nil || target < 0 || target > 256 {
				return 0, 0, false, fmt.Errorf("invalid target difficulty")
			}
		}
		present = true
	}
	return
}

// Mine mutates an unsigned event until its NIP-01 ID reaches target. It does
// not sign the event; callers sign only after mining because signatures are not
// part of the proof.
func Mine(ctx context.Context, event *nostr.Event, target int) error {
	if event == nil || target < 0 || target > 256 {
		return fmt.Errorf("invalid NIP-13 target")
	}
	if event.PubKey == nostr.ZeroPK {
		return fmt.Errorf("event pubkey must be set before NIP-13 mining")
	}
	tags := make(nostr.Tags, 0, len(event.Tags)+1)
	for _, tag := range event.Tags {
		if len(tag) == 0 || tag[0] != "nonce" {
			tags = append(tags, tag)
		}
	}
	tags = append(tags, nostr.Tag{"nonce", "0", strconv.Itoa(target)})
	event.Tags = tags
	for nonce := uint64(0); ; nonce++ {
		if nonce&0x3fff == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if nonce > 0 {
				event.CreatedAt = nostr.Now()
			}
		}
		event.Tags[len(event.Tags)-1][1] = strconv.FormatUint(nonce, 10)
		event.SetID()
		if Difficulty(event.ID) >= target {
			return nil
		}
	}
}

func Validate(event nostr.Event, minimum int, requireCommitment bool) error {
	if minimum < 0 || minimum > 256 || !event.CheckID() {
		return fmt.Errorf("invalid NIP-13 event ID")
	}
	target, _, present, err := Commitment(event)
	if err != nil {
		return err
	}
	if requireCommitment && !present {
		return fmt.Errorf("nonce commitment required")
	}
	if present && target < minimum {
		return fmt.Errorf("committed difficulty %d below required %d", target, minimum)
	}
	if got := Difficulty(event.ID); got < minimum {
		return fmt.Errorf("proof difficulty %d below required %d", got, minimum)
	}
	return nil
}

func MineAndSign(ctx context.Context, keyer nostr.Signer, event *nostr.Event, target int) error {
	if keyer == nil || event == nil {
		return fmt.Errorf("signer and event are required")
	}
	pubkey, err := keyer.GetPublicKey(ctx)
	if err != nil {
		return err
	}
	event.PubKey = pubkey
	if err := Mine(ctx, event, target); err != nil {
		return err
	}
	return keyer.SignEvent(ctx, event)
}
