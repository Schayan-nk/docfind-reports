package find

import (
	"testing"
	"time"
)

// The limiter has to be invisible to a person and fatal to a loop.
//
// That is the only distinction it draws, and both halves matter: a limit that
// catches a runaway script but also catches an administrator working through a
// report is a limit somebody will ask to have removed, and they will be right.
func TestQueryLimiterCatchesALoopAndNotAPerson(t *testing.T) {
	q := newQueryLimiter()

	// A person: run a query, read the answer, run another. Sixty of those
	// inside one minute is already faster than anyone reads, and all sixty are
	// allowed.
	for i := 0; i < queryBurst; i++ {
		if ok, _ := q.allow("boss"); !ok {
			t.Fatalf("query %d of the allowance was refused; a person doing steady work "+
				"must never see this", i+1)
		}
	}

	// The next one is not.
	ok, wait := q.allow("boss")
	if ok {
		t.Error("the allowance can be exceeded, so a loop is not caught at all")
	}
	if wait <= 0 || wait > queryWindow {
		t.Errorf("Retry-After would be %s, which is outside the window", wait)
	}

	// And the limit is per account: the second administrator is not punished
	// for the first one's work. Two people behind one office NAT are two
	// people, which is why this is keyed on the name and not the address.
	if ok, _ := q.allow("someone-else"); !ok {
		t.Error("one account's queries counted against another's allowance")
	}
}

// The window slides rather than resetting.
//
// A counter reset on the minute lets twice the limit through across the
// boundary -- fifty-nine at the end of one minute and sixty at the start of
// the next -- which is precisely the burst this exists to stop.
func TestQueryLimiterWindowSlides(t *testing.T) {
	q := newQueryLimiter()

	// Fill the allowance with timestamps that are already nearly expired.
	old := time.Now().Add(-queryWindow + 80*time.Millisecond)
	stamps := make([]time.Time, queryBurst)
	for i := range stamps {
		stamps[i] = old
	}
	q.seen["boss"] = stamps

	// Refused now, because the window still holds them.
	if ok, _ := q.allow("boss"); ok {
		t.Fatal("a full allowance was not enforced")
	}

	// Once they age out the account is free again, without anything having
	// been reset on a boundary.
	time.Sleep(200 * time.Millisecond)
	if ok, _ := q.allow("boss"); !ok {
		t.Error("the oldest queries left the window but the account is still refused, " +
			"so this is a lockout rather than a rate limit")
	}
}
