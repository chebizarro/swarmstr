package state

// isNewerEvent returns true if candidate should replace current as the latest
// deterministic winner for replaceable state convergence.
//
// Ordering:
//  1. Higher CreatedAt wins
//  2. If CreatedAt ties, lexicographically larger ID wins
func isNewerEvent(candidate, current Event) bool {
	if candidate.CreatedAt != current.CreatedAt {
		return candidate.CreatedAt > current.CreatedAt
	}
	return candidate.ID > current.ID
}
