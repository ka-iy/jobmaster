// Package authz contains a simulated authorization system for clients/user and their roles.
// In a real-life production system, these would come from a full-fledged RBAC/IAM mechanism.
package authz

import "strings"

// The client roles defined in the system.
type Role string

const (
	Unauthorized Role = "Unauthorized"
	Admin        Role = "Admin"
	User         Role = "User"
)

// authorizedClients is a list of clients who are authorized to make RPC calls.
// The "superuser" user is a special user who can access the jobs of all other
// clients. Client names are case-insensitive.
// TODO: Get this from an actual production RBAC/IAM system.
var authorizedClients = map[string]Role{
	"client1":   User,
	"client2":   User,
	"client3":   User,
	"superuser": Admin,
}

// IsClientAuthorized checks whether clientName is an authorized user of the system.
// The check for clientName is case-insensitive.
func IsClientAuthorized(clientName string) (bool, Role) {
	crole := authorizedClients[strings.ToLower(clientName)]
	if crole == "" {
		// Accessing a non-existent map key returns the zero-value for the map's value type.
		crole = Unauthorized
	}
	return (crole != Unauthorized), crole
}

// RoleStrToRole converts roleStr to a Role corresponding to the client roles defined in the system.
func RoleStrToRole(roleStr string) Role {
	switch strings.ToUpper(roleStr) {
	case "ADMIN":
		return Admin
	case "USER":
		return User
	default:
		return Unauthorized
	}
}

// GetAuthorizedClientNames returns a list of names of the authorized clients/users in the system.
func GetAuthorizedClientNames() []string {
	// A slightly better method than using append when iterating through the map, according to SO.
	clientNames := make([]string, len(authorizedClients))
	i := 0
	for key := range authorizedClients {
		clientNames[i] = key
		i++
	}

	return clientNames
}

// GetAuthorizedClientsWithRoles returns a map of authorized clients and their corresponding roles.
func GetAuthorizedClientsWithRoles() map[string]Role {
	return authorizedClients
}
