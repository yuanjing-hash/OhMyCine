package authz

import "testing"

func TestCatalogContainsStableImplementationCodes(t *testing.T) {
	codes := []string{
		PermissionSystemAdmin, PermissionDashboardRead,
		PermissionUsersRead, PermissionUsersCreate, PermissionUsersUpdate, PermissionUsersDisable, PermissionUsersDelete,
		PermissionRolesRead, PermissionRolesCreate, PermissionRolesUpdate, PermissionRolesDelete, PermissionRolesAssign,
		PermissionAuditRead,
		PermissionStoragesRead, PermissionStoragesBrowse, PermissionStoragesCreate, PermissionStoragesUpdate, PermissionStoragesDelete, PermissionStoragesTest,
		PermissionMediaClassificationProfilesRead, PermissionMediaClassificationProfilesCreate, PermissionMediaClassificationProfilesUpdate, PermissionMediaClassificationProfilesDelete,
	}
	for _, code := range codes {
		if !Contains(code) {
			t.Fatalf("implementation permission %q is missing from catalog", code)
		}
	}
}
