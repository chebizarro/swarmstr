package runtime

import "time"

const inboundEventMaxFutureSkew = 30 * time.Second

func timestampTooFarFuture(createdAt int64, now time.Time, maxSkew time.Duration) bool {
	if maxSkew < 0 {
		return false
	}
	return time.Unix(createdAt, 0).After(now.Add(maxSkew))
}

func timestampTooOld(createdAt int64, now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	return time.Unix(createdAt, 0).Before(now.Add(-maxAge))
}
