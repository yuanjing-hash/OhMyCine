package authz

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	PermissionSystemAdmin                       = "system.admin"
	PermissionDashboardRead                     = "dashboard.read"
	PermissionUsersRead                         = "users.read"
	PermissionUsersCreate                       = "users.create"
	PermissionUsersUpdate                       = "users.update"
	PermissionUsersDisable                      = "users.disable"
	PermissionUsersDelete                       = "users.delete"
	PermissionRolesRead                         = "roles.read"
	PermissionRolesCreate                       = "roles.create"
	PermissionRolesUpdate                       = "roles.update"
	PermissionRolesDelete                       = "roles.delete"
	PermissionRolesAssign                       = "roles.assign"
	PermissionAuditRead                         = "audit.read"
	PermissionLogsRead                          = "logs.read"
	PermissionLogsExport                        = "logs.export"
	PermissionLogsConfigure                     = "logs.configure"
	PermissionConnectionsRead                   = "connections.read"
	PermissionConnectionsCreate                 = "connections.create"
	PermissionConnectionsUpdate                 = "connections.update"
	PermissionConnectionsDelete                 = "connections.delete"
	PermissionConnectionsTest                   = "connections.test"
	PermissionDownloadersRead                   = "downloaders.read"
	PermissionDownloadersCreate                 = "downloaders.create"
	PermissionDownloadersUpdate                 = "downloaders.update"
	PermissionDownloadersDelete                 = "downloaders.delete"
	PermissionDownloadersTest                   = "downloaders.test"
	PermissionStoragesRead                      = "storages.read"
	PermissionStoragesBrowse                    = "storages.browse"
	PermissionStoragesCreate                    = "storages.create"
	PermissionStoragesUpdate                    = "storages.update"
	PermissionStoragesDelete                    = "storages.delete"
	PermissionStoragesTest                      = "storages.test"
	PermissionMediaClassificationProfilesRead   = "media_classification_profiles.read"
	PermissionMediaClassificationProfilesCreate = "media_classification_profiles.create"
	PermissionMediaClassificationProfilesUpdate = "media_classification_profiles.update"
	PermissionMediaClassificationProfilesDelete = "media_classification_profiles.delete"
	PermissionMediaLibrariesRead                = "media_libraries.read"
	PermissionMediaLibrariesCreate              = "media_libraries.create"
	PermissionMediaLibrariesUpdate              = "media_libraries.update"
	PermissionMediaLibrariesDelete              = "media_libraries.delete"
	PermissionMediaLibrariesScan                = "media_libraries.scan"
	PermissionMediaServersRefresh               = "media_servers.refresh"
	PermissionJobsReadOwn                       = "jobs.read_own"
	PermissionJobsReadAll                       = "jobs.read_all"
	PermissionJobsControlOwn                    = "jobs.control_own"
	PermissionJobsControlAll                    = "jobs.control_all"
	PermissionJobsRespond                       = "jobs.respond"
	PermissionJobsReorder                       = "jobs.reorder"
	PermissionQueuePoliciesManage               = "queue_policies.manage"
	PermissionDownloadsReadOwn                  = "downloads.read_own"
	PermissionDownloadsReadAll                  = "downloads.read_all"
	PermissionDownloadsCreate                   = "downloads.create"
	PermissionDownloadsManageAll                = "downloads.manage_all"
	PermissionTransfersReadOwn                  = "transfers.read_own"
	PermissionTransfersReadAll                  = "transfers.read_all"
	PermissionDiscoveryRead                     = "discovery.read"
	PermissionSitesRead                         = "sites.read"
	PermissionSitesCreate                       = "sites.create"
	PermissionSitesUpdate                       = "sites.update"
	PermissionSitesDelete                       = "sites.delete"
	PermissionSitesTest                         = "sites.test"
	PermissionSettingsRead                      = "settings.read"
	PermissionSettingsUpdate                    = "settings.update"
	PermissionPluginsRead                       = "plugins.read"
	PermissionPluginsInstall                    = "plugins.install"
	PermissionSTRMRunsRead                      = "strm.runs.read"
	PermissionSTRMRunsCreate                    = "strm.runs.create"
	PermissionSTRMRunsCancel                    = "strm.runs.cancel"
	PermissionSTRMCleanup                       = "strm.cleanup"
	RoleAdministrator                           = "administrator"
	RoleOperator                                = "operator"
	RoleViewer                                  = "viewer"
)

//go:embed catalog.json
var catalogJSON []byte

// Permission describes one stable authorization capability.
type Permission struct {
	Code        string `json:"code"`
	Module      string `json:"module"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

// Catalog parses and validates the canonical permission catalog.
func Catalog() ([]Permission, error) {
	var permissions []Permission
	if err := json.Unmarshal(catalogJSON, &permissions); err != nil {
		return nil, fmt.Errorf("parse permission catalog: %w", err)
	}
	seen := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		if permission.Code == "" || permission.Module == "" || permission.Name == "" {
			return nil, fmt.Errorf("permission catalog contains an incomplete entry")
		}
		if _, exists := seen[permission.Code]; exists {
			return nil, fmt.Errorf("duplicate permission code %q", permission.Code)
		}
		seen[permission.Code] = struct{}{}
	}
	sort.Slice(permissions, func(i, j int) bool { return permissions[i].Code < permissions[j].Code })
	return permissions, nil
}

// Codes returns all canonical permission codes in sorted order.
func Codes() ([]string, error) {
	permissions, err := Catalog()
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		codes = append(codes, permission.Code)
	}
	return codes, nil
}

// Contains reports whether a code exists in the canonical catalog.
func Contains(code string) bool {
	permissions, err := Catalog()
	if err != nil {
		return false
	}
	for _, permission := range permissions {
		if permission.Code == code {
			return true
		}
	}
	return false
}
