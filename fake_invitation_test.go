package main

import (
	"fmt"
	"net/http"
	"slices"
)

// manageInvitations answers InviteMember and UpdateInvitation from what a test seeds: the roles in f.roles, the rows in
// invitationRows and the refusal in refuseInvitation.
func (f *fakeMultiPaaS) manageInvitations(w http.ResponseWriter, operation string, parts []string, variables map[string]any) {
	input, _ := variables["input"].(map[string]any)
	f.mutations = append(f.mutations, variables)
	if f.refuseInvitation[0] != "" {
		graphQLRefusal(w, f.refuseInvitation[0], f.refuseInvitation[1])
		return
	}
	roles := f.rolesByID(input["roleIds"])
	if operation == "UpdateInvitation" {
		f.updateInvitation(w, input, roles)
		return
	}
	f.invitationSeq++
	invitation := map[string]any{
		"id": fmt.Sprintf("invitation-%d", f.invitationSeq), "email": input["email"], "kind": "email", "status": "pending",
		"expiresAt": "2026-11-29T00:00:00.000Z", "appTenant": map[string]any{"id": parts[2]}, "roles": roles,
		"groups": []map[string]any{}, "dataImport": nil,
	}
	f.invitationRows = append(f.invitationRows, invitation)
	f.emailed = append(f.emailed, fmt.Sprint(input["email"]))
	writeData(w, map[string]any{"inviteMember": invitation})
}

// updateInvitation replaces the roles of the seeded invitation and leaves its groups as seeded.
func (f *fakeMultiPaaS) updateInvitation(w http.ResponseWriter, input map[string]any, roles []map[string]any) {
	i := slices.IndexFunc(f.invitationRows, func(r map[string]any) bool { return r["id"] == input["id"] })
	if i < 0 {
		graphQLRefusal(w, "NOT_FOUND", "not found")
		return
	}
	f.invitationRows[i]["roles"] = roles
	writeData(w, map[string]any{"updateInvitation": f.invitationRows[i]})
}

// rolesByID is the seeded roles whose ids were sent, as an invitation lists them.
func (f *fakeMultiPaaS) rolesByID(sent any) []map[string]any {
	roles := []map[string]any{}
	ids, _ := sent.([]any)
	for _, id := range ids {
		if i := slices.IndexFunc(f.roles, func(r map[string]any) bool { return r["id"] == id }); i >= 0 {
			roles = append(roles, map[string]any{"id": f.roles[i]["id"], "key": f.roles[i]["key"], "name": f.roles[i]["name"]})
		}
	}
	return roles
}
