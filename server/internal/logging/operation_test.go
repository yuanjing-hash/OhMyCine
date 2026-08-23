package logging

import "testing"

func TestOperationForHTTPRouteCoversImplementedModules(t *testing.T) {
	tests := map[string]Operation{
		"/api/v1/health":                                             OperationSystemHealth,
		"/api/v1/auth/login":                                         OperationAuthentication,
		"/api/v1/dashboard":                                          OperationDashboard,
		"/api/v1/users/:id":                                          OperationAccessControl,
		"/api/v1/audit":                                              OperationAuditLog,
		"/api/v1/filesystem/directories":                             OperationDirectoryBrowsing,
		"/api/v1/connections/:id/directories":                        OperationDirectoryBrowsing,
		"/api/v1/connections/:id/test":                               OperationConnectionManagement,
		"/api/v1/storages/:id/test":                                  OperationStorageManagement,
		"/api/v1/media-classification-profiles/:id":                  OperationMediaRuleManagement,
		"/api/v1/media-libraries/:id/catalog":                        OperationMediaLibraryManagement,
		"/api/v1/jobs/:id/retry":                                     OperationTaskQueue,
		"/api/v1/transfers/:id":                                      OperationMediaTransfer,
		"/api/v1/downloaders/:id/test":                               OperationDownloaderManagement,
		"/api/v1/downloads/:id":                                      OperationDownloadTask,
		"/api/v1/player/online-libraries/:id/items/:itemId/download": OperationPluginDownload,
		"/api/v1/seeding-tasks/:id/stop":                             OperationSeedingManagement,
		"/api/v1/settings/downloads":                                 OperationSystemSettings,
		"/api/v1/settings/metadata/test":                             OperationMetadataConfiguration,
		"/api/v1/runtime-logs/facets":                                OperationRuntimeLogging,
		"/api/v1/plugin-repositories/:id/refresh":                    OperationPluginRepository,
		"/api/v1/plugins/marketplace":                                OperationPluginRepository,
		"/proxy/strm/:opaque":                                        OperationSignedProxy,
		"/emby/:gateway/*path":                                       OperationEmbyProxyGateway,
	}
	for route, want := range tests {
		if got := OperationForHTTPRoute(route); got != want {
			t.Errorf("route %q operation=%+v want=%+v", route, got, want)
		}
	}
}

func TestArtifactAndProxyOperationsHaveStableLocalizedLabels(t *testing.T) {
	tests := map[string]Operation{
		"metadata_snapshot":            OperationMetadataSnapshot,
		"media_artifact":               OperationMediaArtifact,
		"strm_generation":              OperationSTRMGeneration,
		"signed_proxy":                 OperationSignedProxy,
		"emby_proxy_gateway":           OperationEmbyProxyGateway,
		"pan115_sidecar_upload":        OperationPan115SidecarUpload,
		"pan115_multi_device_playback": OperationPan115MultiDevicePlayback,
		"pan115_playback_cleanup":      OperationPan115PlaybackCleanup,
		"artifact_cleanup":             OperationArtifactCleanup,
		"download_staging_cleanup":     OperationDownloadStagingCleanup,
	}
	for code, operation := range tests {
		if operation.Code != code || operation.Label == "" || operation.Message("开始")[:len("【")] != "【" {
			t.Fatalf("operation %q is not stable/localized: %+v", code, operation)
		}
	}
}
