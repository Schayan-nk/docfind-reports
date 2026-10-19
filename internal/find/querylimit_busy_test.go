package find

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
)

// A second builder query from the same person while the first still runs is
// refused; somebody else is not affected; and once the first ends the next
// one runs.
func TestOneBuilderQueryAtATimePerPerson(t *testing.T) {
	s, _, _, _ := builderTestServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	h := s.oneAtATime(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("slow") == "1" {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	as := func(user, q string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, auth.WithUser(httptest.NewRequest("POST", "/api/builder/run"+q, nil), &auth.User{Username: user}))
		return rec
	}

	done := make(chan int)
	go func() { done <- as("boss", "?slow=1").Code }()
	<-started
	if code := as("boss", "").Code; code != http.StatusTooManyRequests {
		t.Errorf("a second query while the first runs answered %d, want 429", code)
	}
	if code := as("reader", "").Code; code != http.StatusOK {
		t.Errorf("somebody else was held up: %d", code)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("the first query answered %d", code)
	}
	if code := as("boss", "").Code; code != http.StatusOK {
		t.Errorf("after the first finished the next answered %d", code)
	}
}
