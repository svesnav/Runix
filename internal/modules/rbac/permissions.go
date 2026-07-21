package rbac

// The permission catalog. Permissions are code, not data: modules check
// against these constants and the database only ever references them.
const (
	PermServerView       = "server.view"
	PermServerCreate     = "server.create"
	PermServerUpdate     = "server.update"
	PermServerDelete     = "server.delete"
	PermServerPower      = "server.power"
	PermServerConsole    = "server.console"
	PermServerFilesRead  = "server.files.read"
	PermServerFilesWrite = "server.files.write"
	PermServerLogs       = "server.logs"
	PermServerMetrics    = "server.metrics"

	PermRuntimeManage  = "runtime.manage"
	PermRuntimeExecute = "runtime.execute"
	PermDockerManage   = "docker.manage"
	PermComposeManage  = "compose.manage"
	PermSystemdManage  = "systemd.manage"
	PermDaemonManage   = "daemon.manage"

	PermTerminalOpen  = "terminal.open"
	PermTerminalAdmin = "terminal.admin"

	PermSettingsView = "settings.view"
	PermSettingsEdit = "settings.edit"

	PermSchedulerManage = "scheduler.manage"

	PermUsersManage       = "users.manage"
	PermRolesManage       = "roles.manage"
	PermPermissionsManage = "permissions.manage"
	PermAuditView         = "audit.view"

	PermBackupCreate  = "backup.create"
	PermBackupRestore = "backup.restore"

	PermPluginsInstall = "plugins.install"
	PermPluginsManage  = "plugins.manage"
)

var allPermissions = []string{
	PermServerView, PermServerCreate, PermServerUpdate, PermServerDelete,
	PermServerPower, PermServerConsole, PermServerFilesRead, PermServerFilesWrite,
	PermServerLogs, PermServerMetrics,
	PermRuntimeManage, PermRuntimeExecute,
	PermDockerManage, PermComposeManage, PermSystemdManage, PermDaemonManage,
	PermTerminalOpen, PermTerminalAdmin,
	PermSettingsView, PermSettingsEdit, PermSchedulerManage,
	PermUsersManage, PermRolesManage, PermPermissionsManage, PermAuditView,
	PermBackupCreate, PermBackupRestore,
	PermPluginsInstall, PermPluginsManage,
}

// AllPermissions returns the catalog keys (copy, callers cannot mutate it).
func AllPermissions() []string {
	out := make([]string, len(allPermissions))
	copy(out, allPermissions)
	return out
}

// Permission groups, used to organize the catalog for humans.
const (
	GroupServers = "servers"
	GroupFiles   = "files"
	GroupRuntime = "runtime"
	GroupAccess  = "access"
	GroupSystem  = "system"
)

// Descriptor is the human-facing view of a permission. The dotted Key stays
// the canonical identifier stored in the database and checked in code; Label
// and Description exist so operators never have to read machine keys.
type Descriptor struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

var descriptors = []Descriptor{
	{PermServerView, "View servers", "See servers, their status and inventory", GroupServers},
	{PermServerCreate, "Add servers", "Register new servers and issue agent tokens", GroupServers},
	{PermServerUpdate, "Edit servers", "Change server details, tags and agent tokens", GroupServers},
	{PermServerDelete, "Delete servers", "Remove servers and their history", GroupServers},
	{PermServerPower, "Power actions", "Reboot and shut down managed servers", GroupServers},
	{PermServerConsole, "Server console", "Use the server's console output", GroupServers},
	{PermServerMetrics, "View metrics", "See live and historical resource usage", GroupServers},
	{PermServerLogs, "View logs", "Read runtime and service logs", GroupServers},

	{PermServerFilesRead, "Browse files", "List, read and download files", GroupFiles},
	{PermServerFilesWrite, "Manage files", "Create, edit, upload, move and delete files", GroupFiles},

	{PermRuntimeManage, "Manage runtimes", "Create, start, stop and remove any runtime", GroupRuntime},
	{PermRuntimeExecute, "Run commands", "Execute one-off commands inside runtimes", GroupRuntime},
	{PermDockerManage, "Manage Docker", "Containers, images, volumes and networks", GroupRuntime},
	{PermComposeManage, "Manage Compose", "Create, edit and control Compose projects", GroupRuntime},
	{PermSystemdManage, "Manage services", "Control systemd units", GroupRuntime},
	{PermDaemonManage, "Manage daemons", "Create and control native daemons", GroupRuntime},
	{PermTerminalOpen, "Open terminals", "Open a shell inside runtimes", GroupRuntime},
	{PermTerminalAdmin, "Host terminal", "Open a shell on the server itself", GroupRuntime},

	{PermUsersManage, "Manage users", "Create, edit and deactivate user accounts", GroupAccess},
	{PermRolesManage, "Manage roles", "Define roles and their permissions", GroupAccess},
	{PermPermissionsManage, "Manage grants", "Grant scoped permissions to users and groups", GroupAccess},
	{PermAuditView, "View audit log", "Read the record of who did what", GroupAccess},

	{PermSettingsView, "View settings", "See platform configuration", GroupSystem},
	{PermSettingsEdit, "Edit settings", "Change platform configuration", GroupSystem},
	{PermSchedulerManage, "Manage schedule", "Create and run scheduled tasks", GroupSystem},
	{PermBackupCreate, "Create backups", "Export the platform configuration", GroupSystem},
	{PermBackupRestore, "Restore backups", "Import a configuration backup", GroupSystem},
	{PermPluginsInstall, "Install plugins", "Add and remove plugins", GroupSystem},
	{PermPluginsManage, "Manage plugins", "Enable, disable and configure plugins", GroupSystem},
}

// PermissionCatalog returns every permission with its human-readable name.
func PermissionCatalog() []Descriptor {
	out := make([]Descriptor, len(descriptors))
	copy(out, descriptors)
	return out
}

var permissionSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(allPermissions))
	for _, p := range allPermissions {
		m[p] = struct{}{}
	}
	return m
}()

func ValidPermission(p string) bool {
	_, ok := permissionSet[p]
	return ok
}

// Built-in roles, seeded idempotently at startup. System roles cannot be
// deleted and the admin role cannot lose permissions.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

func builtinRoles() map[string][]string {
	return map[string][]string{
		RoleAdmin: AllPermissions(),
		RoleOperator: {
			PermServerView, PermServerPower, PermServerConsole,
			PermServerFilesRead, PermServerFilesWrite, PermServerLogs, PermServerMetrics,
			PermRuntimeManage, PermRuntimeExecute,
			PermDockerManage, PermComposeManage, PermSystemdManage, PermDaemonManage,
			PermTerminalOpen, PermAuditView,
		},
		RoleViewer: {
			PermServerView, PermServerLogs, PermServerMetrics,
		},
	}
}
