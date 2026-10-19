package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Schayan-nk/docfind-reports/internal/auth"
	"github.com/Schayan-nk/docfind-reports/internal/pgstore"
	"github.com/Schayan-nk/docfind-reports/internal/sms"
)

// setPhone is `-set-phone <username> -phone <mobile>`: one account's mobile
// number, written straight to the account database -- the number SMS sign-in
// codes go to.
//
// RUN IT WITH THE SERVER STOPPED. A running server keeps every account in
// memory and writes them all back on its next save, which would put the old
// number back.
func setPhone(username, phone string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	p, ok := sms.NormalizePhone(phone)
	if !ok {
		return fmt.Errorf("%q is not an Iranian mobile number", phone)
	}
	pc := pgstore.FromEnv()
	if !pc.Enabled() {
		return errors.New("accounts are not kept in PostgreSQL here (DOCFIND_PG_* unset); set the number on the users page")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, pc)
	if err != nil {
		return fmt.Errorf("the account database: %w", err)
	}
	defer db.Close()
	found, err := pgstore.NewAccounts(db).SetPhone(ctx, username, p)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no docfind account called %q", username)
	}
	fmt.Printf("%s: mobile set to %s\n", username, p[:4]+strings.Repeat("*", len(p)-8)+p[len(p)-4:])
	return nil
}

// builderPerms are what "-perm builder" stands for: the builder's page and
// its four actions, all explicit, so no role carries them -- not even an
// administrator's.
var builderPerms = []string{auth.PageBuilder, auth.ActionBuilderRun, auth.ActionBuilderSave,
	auth.ActionSources, auth.ActionBuilderExport}

// setGrant is `-set-grant <username> -perm <id>[,<id>...] -state
// allow|deny|inherit`: permission overrides written straight to the account
// database, with the server stopped (as setPhone). Each permission must be one
// the catalogue declares, or a report's; "builder" names the builder's five.
func setGrant(usersFile, username, perm, state string) error {
	var perms []string
	for _, p := range strings.Split(perm, ",") {
		switch p = strings.TrimSpace(p); {
		case p == "":
		case strings.EqualFold(p, "builder"):
			perms = append(perms, builderPerms...)
		default:
			perms = append(perms, p)
		}
	}
	if len(perms) == 0 {
		return errors.New("-perm names no permission")
	}
	for _, p := range perms {
		if err := setOneGrant(usersFile, username, p, state); err != nil {
			return err
		}
	}
	return nil
}

// setOneGrant sets one permission override.
func setOneGrant(usersFile, username, perm, state string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	perm = strings.TrimSpace(perm)
	switch state = strings.ToLower(strings.TrimSpace(state)); state {
	case "allow", "deny":
	case "inherit", "clear", "":
		state = ""
	default:
		return fmt.Errorf("-state is allow, deny or inherit, not %q", state)
	}
	if !auth.NewCatalogue().Has(perm) && !strings.HasPrefix(perm, auth.ReportPrefix) {
		return fmt.Errorf("%q is not a permission docfind declares", perm)
	}
	pc := pgstore.FromEnv()
	if !pc.Enabled() {
		return setGrantInFile(usersFile, username, perm, state)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgstore.Open(ctx, pc)
	if err != nil {
		return fmt.Errorf("the account database: %w", err)
	}
	defer db.Close()
	// A database the server has not started against yet has no schema, and
	// the accounts file is not in it: until then the file is the store, and
	// the first start imports the grant along with the account.
	var ready bool
	if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&ready); err != nil {
		return fmt.Errorf("the account database: %w", err)
	}
	if !ready {
		return setGrantInFile(usersFile, username, perm, state)
	}
	found, err := pgstore.NewAccounts(db).SetGrant(ctx, username, perm, state)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no docfind account called %q", username)
	}
	if state == "" {
		state = "inherit (the role decides)"
	}
	fmt.Printf("%s: %s set to %s\n", username, perm, state)
	return nil
}

// setGrantInFile is setOneGrant where the accounts file is the only store (no
// DOCFIND_PG_*). It is how a lone administrator is given the builder's
// explicit permissions: nobody may change their own in Settings, and with one
// administrator there is nobody else to ask.
func setGrantInFile(usersFile, username, perm, state string) error {
	if usersFile == "" {
		return errors.New("accounts are kept in the accounts file here: name it with -users")
	}
	users, err := auth.OpenStore(usersFile)
	if err != nil {
		return err
	}
	u, err := users.Get(username)
	if err != nil {
		return fmt.Errorf("no docfind account called %q", username)
	}
	grants := map[string]auth.GrantState{}
	for id, st := range u.Grants {
		grants[id] = auth.GrantState(st)
	}
	if state == "" {
		delete(grants, perm)
	} else {
		grants[perm] = auth.GrantState(state)
	}
	if err := users.SetGrants(u.Username, grants); err != nil {
		return err
	}
	if state == "" {
		state = "inherit (the role decides)"
	}
	fmt.Printf("%s: %s set to %s\n", u.Username, perm, state)
	return nil
}
