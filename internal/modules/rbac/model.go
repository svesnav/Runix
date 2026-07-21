package rbac

import "time"

type Role struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsSystem    bool      `json:"isSystem"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

type SubjectType string

const (
	SubjectUser  SubjectType = "user"
	SubjectGroup SubjectType = "group"
	// SubjectRole grants to everyone currently holding a role, which is the
	// natural way to say "operators may restart runtimes on this server".
	SubjectRole SubjectType = "role"
)

type ScopeType string

const (
	ScopeGlobal      ScopeType = "global"
	ScopeServerGroup ScopeType = "server_group"
	ScopeServer      ScopeType = "server"
	ScopeRuntime     ScopeType = "runtime"
)

// Grant awards one permission to a user or group, optionally narrowed to a
// server group, a server, or a single runtime.
type Grant struct {
	ID          string      `json:"id"`
	SubjectType SubjectType `json:"subjectType"`
	SubjectID   string      `json:"subjectId"`
	Permission  string      `json:"permission"`
	ScopeType   ScopeType   `json:"scopeType"`
	ScopeID     string      `json:"scopeId,omitempty"`
	CreatedAt   time.Time   `json:"createdAt"`
	CreatedBy   string      `json:"createdBy,omitempty"`
}

// Scope is what a permission check is evaluated against.
type Scope struct {
	Type ScopeType
	ID   string
}

var GlobalScope = Scope{Type: ScopeGlobal}

func ServerScope(serverID string) Scope {
	return Scope{Type: ScopeServer, ID: serverID}
}

// RuntimeScope addresses a single runtime. The id includes the server so a
// grant for "docker/web" on one host never leaks to another host's
// identically named runtime.
func RuntimeScope(serverID, runtimeType, runtimeID string) Scope {
	return Scope{Type: ScopeRuntime, ID: RuntimeScopeID(serverID, runtimeType, runtimeID)}
}

func RuntimeScopeID(serverID, runtimeType, runtimeID string) string {
	return serverID + "/" + runtimeType + "/" + runtimeID
}
