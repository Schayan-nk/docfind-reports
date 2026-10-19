package main

import "github.com/Schayan-nk/docfind-reports/internal/reports"

// reportPerms turns the report catalogue into the two lists the permission
// catalogue wants: the IDs to make grantable, and their Persian titles.
//
// Here rather than in either package, for the same reason the customer sync's
// mapping is: internal/auth must not know what a report is, and
// internal/reports must not know what a permission is. This is the only place
// that knows both.
//
// The TITLE is used as the label because it is what the person granting access
// sees on the dashboard. A permission screen that calls a report something
// different from the page it appears on is one people set wrongly.
func reportPerms(cat *reports.Catalog) ([]string, map[string]string) {
	if cat == nil {
		return nil, nil
	}
	list := cat.List()
	ids := make([]string, 0, len(list))
	labels := make(map[string]string, len(list))
	for _, r := range list {
		if r.ID == "" {
			continue
		}
		ids = append(ids, r.ID)
		labels[r.ID] = r.Title
	}
	return ids, labels
}

// reportGroups is the heading each report is filed under on the permission
// screen, for the reports that declare one ("-- group: حسابداری") -- so the
// accounting reports are granted under the name the menu gives them.
func reportGroups(cat *reports.Catalog) map[string]string {
	if cat == nil {
		return nil
	}
	out := map[string]string{}
	for _, r := range cat.List() {
		if r.Group != "" {
			out[r.ID] = r.Group
		}
	}
	return out
}
