package state

import "sort"

func sortEventsNewestFirst(events []Event) {
	sort.Slice(events, func(i, j int) bool {
		return isNewerEvent(events[i], events[j])
	})
}

func filterEventsForPage(events []Event, cursor *EventPageCursor) []Event {
	if cursor == nil || cursor.Until <= 0 {
		return append([]Event(nil), events...)
	}
	skip := make(map[string]struct{}, len(cursor.SkipIDs))
	for _, id := range cursor.SkipIDs {
		if id == "" {
			continue
		}
		skip[id] = struct{}{}
	}
	out := make([]Event, 0, len(events))
	for _, evt := range events {
		if evt.CreatedAt > cursor.Until {
			continue
		}
		if evt.CreatedAt == cursor.Until {
			if _, ok := skip[evt.ID]; ok {
				continue
			}
		}
		out = append(out, evt)
	}
	return out
}

func nextCursorForPage(current *EventPageCursor, page []Event) *EventPageCursor {
	if len(page) == 0 {
		return nil
	}
	boundaryUnix := page[len(page)-1].CreatedAt
	seen := make(map[string]struct{})
	skipIDs := make([]string, 0, len(page))
	if current != nil && current.Until == boundaryUnix {
		for _, id := range current.SkipIDs {
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			skipIDs = append(skipIDs, id)
		}
	}
	for _, evt := range page {
		if evt.CreatedAt != boundaryUnix || evt.ID == "" {
			continue
		}
		if _, ok := seen[evt.ID]; ok {
			continue
		}
		seen[evt.ID] = struct{}{}
		skipIDs = append(skipIDs, evt.ID)
	}
	sort.Strings(skipIDs)
	return &EventPageCursor{
		Until:   boundaryUnix,
		SkipIDs: skipIDs,
	}
}
