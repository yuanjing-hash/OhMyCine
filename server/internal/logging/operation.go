package logging

import (
	"strings"

	"github.com/rs/zerolog"
)

// Operation is a stable machine-readable business operation with a localized
// label intended for the runtime log UI.
type Operation struct {
	Code  string
	Label string
}

var (
	OperationServerLifecycle           = Operation{"server_lifecycle", "服务生命周期"}
	OperationHTTPRequest               = Operation{"http_request", "HTTP请求"}
	OperationSystemHealth              = Operation{"system_health", "系统健康"}
	OperationAuthentication            = Operation{"authentication", "认证与初始化"}
	OperationDashboard                 = Operation{"dashboard", "仪表盘"}
	OperationAccessControl             = Operation{"access_control", "用户与权限"}
	OperationAuditLog                  = Operation{"audit_log", "审计日志"}
	OperationStorageManagement         = Operation{"storage_management", "存储管理"}
	OperationDirectoryBrowsing         = Operation{"directory_browsing", "目录浏览"}
	OperationMediaRuleManagement       = Operation{"media_rule_management", "分类规则管理"}
	OperationMediaLibraryManagement    = Operation{"media_library_management", "媒体库管理"}
	OperationConnectionManagement      = Operation{"connection_management", "连接管理"}
	OperationDownloaderManagement      = Operation{"downloader_management", "下载器管理"}
	OperationSystemSettings            = Operation{"system_settings", "系统设置"}
	OperationMetadataConfiguration     = Operation{"metadata_configuration", "元数据设置"}
	OperationRuntimeLogging            = Operation{"runtime_logging", "运行日志"}
	OperationConnectionProbe           = Operation{"connection_probe", "连接检测"}
	OperationProviderLifeEvent         = Operation{"provider_life_event", "115生活事件"}
	OperationLibraryInitialScan        = Operation{"library_initial_scan", "媒体库首次扫描"}
	OperationLibraryIncrementalScan    = Operation{"library_incremental_scan", "媒体库增量扫描"}
	OperationLibraryFullScan           = Operation{"library_full_scan", "媒体库全量扫描"}
	OperationLibraryEventScan          = Operation{"library_event_scan", "媒体库事件增量"}
	OperationMediaRecognition          = Operation{"media_recognition", "媒体识别"}
	OperationMetadataSnapshot          = Operation{"metadata_snapshot", "元数据快照"}
	OperationMediaArtifact             = Operation{"media_artifact", "媒体产物"}
	OperationSTRMGeneration            = Operation{"strm_generation", "STRM生成"}
	OperationIncrementalSTRMGeneration = Operation{"incremental_strm_generation", "增量STRM生成"}
	OperationFullSTRMGeneration        = Operation{"full_strm_generation", "全量STRM生成"}
	OperationSignedProxy               = Operation{"signed_proxy", "302代理"}
	OperationEmbyProxyGateway          = Operation{"emby_proxy_gateway", "Emby302网关"}
	OperationPan115SidecarUpload       = Operation{"pan115_sidecar_upload", "115旁挂上传"}
	OperationPan115MultiDevicePlayback = Operation{"pan115_multi_device_playback", "115多设备播放"}
	OperationPan115PlaybackCleanup     = Operation{"pan115_playback_cleanup", "115临时副本清理"}
	OperationArtifactCleanup           = Operation{"artifact_cleanup", "产物清理"}
	OperationDownloadStagingCleanup    = Operation{"download_staging_cleanup", "下载暂存清理"}
	OperationDownloadTask              = Operation{"download_task", "下载任务"}
	OperationDownloadClassification    = Operation{"download_classification", "下载预分类"}
	OperationPan115OfflineDownload     = Operation{"pan115_offline_download", "115离线下载"}
	OperationPan115ShareIngest         = Operation{"pan115_share_ingest", "115分享摄取"}
	OperationPan115CloudTransfer       = Operation{"pan115_cloud_transfer", "115云端整理"}
	OperationMediaTransfer             = Operation{"media_transfer", "媒体整理"}
	OperationSeedingManagement         = Operation{"seeding_management", "做种管理"}
	OperationTaskQueue                 = Operation{"task_queue", "系统任务队列"}
	OperationPluginRepository          = Operation{"plugin_repository", "插件仓库"}
	OperationPluginRuntime             = Operation{"plugin_runtime", "插件运行时"}
)

func (o Operation) Event(event *zerolog.Event) *zerolog.Event {
	return event.Str("operation", o.Code).Str("operation_label", o.Label)
}

func (o Operation) Message(message string) string {
	return "【" + o.Label + "】" + message
}

// OperationForHTTPRoute gives every implemented API surface a user-visible
// business module while preserving normalized routes and safe request fields.
func OperationForHTTPRoute(route string) Operation {
	route = strings.TrimSpace(route)
	if strings.HasPrefix(route, "/proxy/strm") {
		return OperationSignedProxy
	}
	if strings.HasPrefix(route, "/emby/:gateway") {
		return OperationEmbyProxyGateway
	}
	route = strings.TrimPrefix(route, "/api/v1")
	switch {
	case route == "/health":
		return OperationSystemHealth
	case strings.HasPrefix(route, "/setup"), strings.HasPrefix(route, "/auth"):
		return OperationAuthentication
	case route == "/dashboard":
		return OperationDashboard
	case route == "/permissions", strings.HasPrefix(route, "/users"), strings.HasPrefix(route, "/roles"):
		return OperationAccessControl
	case strings.HasPrefix(route, "/audit"):
		return OperationAuditLog
	case strings.HasPrefix(route, "/filesystem"), strings.Contains(route, "/directories"), strings.HasSuffix(route, "/directory"):
		return OperationDirectoryBrowsing
	case strings.HasPrefix(route, "/connections"):
		return OperationConnectionManagement
	case strings.HasPrefix(route, "/storages"):
		return OperationStorageManagement
	case strings.HasPrefix(route, "/media-classification-profiles"):
		return OperationMediaRuleManagement
	case strings.HasPrefix(route, "/media-libraries") && strings.Contains(route, "/recognitions"):
		return OperationMediaRecognition
	case strings.HasPrefix(route, "/media-libraries"):
		return OperationMediaLibraryManagement
	case strings.HasPrefix(route, "/jobs"), strings.HasPrefix(route, "/job-lanes"), strings.HasPrefix(route, "/queue"):
		return OperationTaskQueue
	case strings.HasPrefix(route, "/transfers"):
		return OperationMediaTransfer
	case strings.HasPrefix(route, "/downloaders"):
		return OperationDownloaderManagement
	case strings.HasPrefix(route, "/downloads"):
		return OperationDownloadTask
	case strings.HasPrefix(route, "/seeding-tasks"), strings.HasPrefix(route, "/settings/seeding"):
		return OperationSeedingManagement
	case strings.HasPrefix(route, "/settings/metadata"):
		return OperationMetadataConfiguration
	case strings.HasPrefix(route, "/settings"):
		return OperationSystemSettings
	case strings.HasPrefix(route, "/runtime-logs"):
		return OperationRuntimeLogging
	case strings.HasPrefix(route, "/plugin-repositories"), route == "/plugins/marketplace":
		return OperationPluginRepository
	case strings.HasPrefix(route, "/plugins"):
		return OperationPluginRuntime
	default:
		return OperationHTTPRequest
	}
}
