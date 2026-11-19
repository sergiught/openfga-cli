// Package auth0 reads an Auth0 tenant through the official Go SDK and returns
// its current state as snapshot events.
package auth0

import "github.com/sergiught/openfga-cli/internal/mapping/snapshot"

const (
	typeConnection         = "snapshot.connection"
	typeUser               = "snapshot.user"
	typeOrganization       = "snapshot.organization"
	typeOrgConnection      = "snapshot.organization.connection"
	typeOrgMember          = "snapshot.organization.member"
	typeOrgMemberRole      = "snapshot.organization.member.role"
	maxUsers               = 1000
	pageSize               = 100
	scopeReadOrganizations = "read:organizations"
)

// Types lists the snapshot event types an Auth0 tenant can produce, in the
// order their events are emitted. It needs no credentials.
func Types() []snapshot.Type {
	return []snapshot.Type{
		{Name: typeConnection, Mirrors: "connection.created",
			Description: "each connection, with the clients it is enabled for",
			Scopes:      []string{"read:connections"}},
		{Name: typeUser, Mirrors: "user.created",
			Description: "each user",
			Scopes:      []string{"read:users"}},
		{Name: typeOrganization, Mirrors: "organization.created",
			Description: "each organization",
			Scopes:      []string{scopeReadOrganizations}},
		{Name: typeOrgConnection, Mirrors: "organization.connection.added",
			Description: "each connection enabled on an organization",
			Scopes:      []string{scopeReadOrganizations, "read:organization_connections"}},
		{Name: typeOrgMember, Mirrors: "organization.member.added",
			Description: "each member of an organization",
			Scopes:      []string{scopeReadOrganizations, "read:organization_members"}},
		{Name: typeOrgMemberRole, Mirrors: "organization.member.role.assigned",
			Description: "each role a member holds in an organization",
			Scopes: []string{
				scopeReadOrganizations, "read:organization_members", "read:organization_member_roles",
			}},
	}
}
