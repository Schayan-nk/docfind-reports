package find

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/Schayan-nk/docfind-reports/internal/netguard"
	"github.com/Schayan-nk/docfind-reports/internal/reports"
)

// Hosts somebody typed into a form.
//
// The report builder opens a connection to a host a person chose, which is a
// way to make this server connect where its user cannot: its own loopback
// services, or a metadata address. So the host is checked by the addresses the
// name RESOLVES to (internal/netguard): the network is fine, this machine and
// link-local addresses are not. Everything set in the .env is the operator's
// configuration and is not second-guessed here.
//
// DOCFIND_ALLOWED_HOSTS is a comma-separated list of hosts accepted whatever
// they resolve to, for the day a database has to be reached by a name this
// would otherwise refuse -- one running beside this server, say.

// allowedHostsFromEnv reads DOCFIND_ALLOWED_HOSTS.
func allowedHostsFromEnv() []string {
	var out []string
	for _, h := range strings.Split(os.Getenv("DOCFIND_ALLOWED_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// builderGuard is the report builder's rule, installed on the source store so
// testing a new connection and opening a saved one both go through it.
func (s *Server) builderGuard(ctx context.Context, driver reports.Driver, dsn string) error {
	if reports.DSNReadsLocalFiles(driver, dsn) {
		return errors.New("allowAllFiles به سرور پایگاه داده اجازه می‌دهد فایل‌های این سرور را بخواند؛ آن را از رشتهٔ اتصال بردارید.")
	}
	host := reports.DSNHost(driver, dsn)
	err := netguard.CheckHost(ctx, host, netguard.LAN, s.allowedHosts...)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, netguard.ErrUnresolved):
		return errors.New("سرور پایگاه داده پیدا نشد: " + host)
	}
	return errors.New("اتصال به " + host + " مجاز نیست: سازندهٔ گزارش فقط به سرورهای شبکه وصل می‌شود، نه به خود این سرور. " +
		"اگر لازم است، نام آن را در DOCFIND_ALLOWED_HOSTS بگذارید.")
}
