package find

import (
	"net/http"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
)

// requireBuilderPage serves the builder's page to those who hold page:builder
// and sends anyone else back to the reports: their menu omits it, and typing
// the address must not land them on a page whose every request would be
// refused.
func (s *Server) requireBuilderPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.AuthEnabled() {
			next(w, r)
			return
		}
		r2, ok := s.guard.Authenticate(r)
		if !ok {
			http.Redirect(w, r, "/login?next=%2Fbuilder", http.StatusSeeOther)
			return
		}
		if !s.can(r2, auth.PageBuilder) {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		next(w, r2)
	}
}
