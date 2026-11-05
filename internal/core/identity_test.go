package core

import "testing"

// In Microsoft Entra any member can usually create a group and pick its
// display name. A group named after another group's object ID must not
// count as membership of that group (sharing by group stores the ID).
func TestInGroupDisplayNameCantSpoofObjectID(t *testing.T) {
	const finance = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	attacker := &User{Groups: []Group{{ID: "9a8b7c6d-0000-4000-8000-000000000001", Name: finance}, {ID: "9a8b7c6d-0000-4000-8000-000000000002", Name: "3F2504E0-4F89-11D3-9A0C-0305E82C3301"}}}
	if attacker.InGroup(finance) {
		t.Fatal("a group whose display name is another group's object ID matched it")
	}
	member := &User{Groups: []Group{{ID: finance, Name: "Finance"}}}
	if !member.InGroup(finance) || !member.InGroup("finance") {
		t.Fatal("real member not matched by ID or name")
	}
	// Groups from OIDC claims or invites use the name as the ID.
	local := &User{Groups: []Group{{ID: "Family", Name: "Family"}}}
	if !local.InGroup("family") || !local.InGroup("Family") {
		t.Fatal("name-only group not matched")
	}
	if attacker.InGroupID(finance) || !member.InGroupID(finance) || member.InGroupID("Finance") {
		t.Fatal("InGroupID must match IDs only")
	}
}
