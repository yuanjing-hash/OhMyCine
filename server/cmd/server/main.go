package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
	"github.com/yuanjing-hash/ohmycine/server/internal/httpserver"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	pluginhostapi "github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	pluginrepository "github.com/yuanjing-hash/ohmycine/server/internal/plugins/repository"
	pluginruntime "github.com/yuanjing-hash/ohmycine/server/internal/plugins/runtime"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud/pan115"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader/pan115offline"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader/qbittorrent"
	"github.com/yuanjing-hash/ohmycine/server/pkg/mediatool"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logManager, err := logging.NewManager(cfg.LogDirectory, cfg.Environment, os.Stdout)
	if err != nil {
		panic(err)
	}
	defer logManager.Close()
	log := logManager.Logger("server", "bootstrap")
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "database_open_failed").Msg(logging.OperationServerLifecycle.Message("数据库打开失败"))
	}
	if err := database.Migrate(db); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "database_migration_failed").Msg(logging.OperationServerLifecycle.Message("数据库迁移失败"))
	}
	credentialStore, err := credential.Open(cfg.CredentialKeyFile, cfg.CredentialMasterKey)
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "credential_store_initialization_failed").Msg(logging.OperationServerLifecycle.Message("凭据加密初始化失败"))
	}
	audit := services.NewAuditService(db)
	runtimeLogs, err := services.NewRuntimeLogService(db, logManager, audit)
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "runtime_logging_initialization_failed").Msg(logging.OperationServerLifecycle.Message("运行日志初始化失败"))
	}
	authorization := services.NewAuthorizationService(db)
	auth, err := services.NewAuthService(db, cfg, authorization, audit)
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "authentication_initialization_failed").Msg(logging.OperationServerLifecycle.Message("认证服务初始化失败"))
	}
	admin := services.NewAdminService(db, authorization, auth, audit)
	cloudRegistry := cloudpkg.NewRegistry()
	if err := cloudRegistry.Register(cloudpkg.ProviderPan115, pan115.New); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "pan115_provider_registration_failed").Msg(logging.OperationServerLifecycle.Message("115 驱动注册失败"))
	}
	connections := services.NewConnectionService(db, audit, credentialStore, cloudRegistry, logManager.Logger("connection", "pan115"))
	credentialReveal := services.NewCredentialRevealService(db, audit, credentialStore)
	signedProxy, err := services.NewSignedProxyService(db, credentialStore, connections, cfg.PublicOrigin, logManager.Logger("proxy", "signed_strm"))
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "signed_proxy_initialization_failed").Msg(logging.OperationServerLifecycle.Message("302 代理初始化失败"))
	}
	if err := signedProxy.Start(context.Background()); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "pan115_playback_coordinator_start_failed").Msg(logging.OperationServerLifecycle.Message("115 多设备播放协调器启动失败"))
	}
	defer signedProxy.Close()
	embyGateway, err := services.NewEmbyGatewayService(db, audit, signedProxy, cfg.PublicOrigin, logManager.Logger("proxy", "emby_gateway"))
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "emby_gateway_initialization_failed").Msg(logging.OperationServerLifecycle.Message("Emby 302 网关初始化失败"))
	}
	providerDirectories := services.NewProviderDirectoryService(connections, credentialStore)
	storages := services.NewStorageService(db, audit)
	storages.SetConnectionService(connections)
	directories, err := services.NewDirectoryBrowserService(db, nil)
	if err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "directory_browser_initialization_failed").Msg(logging.OperationServerLifecycle.Message("目录浏览器初始化失败"))
	}
	directories.SetProviderDirectoryService(providerDirectories)
	profiles := services.NewMediaClassificationProfileService(db, audit, nil)
	libraries := services.NewMediaLibraryService(db, audit, logManager.Logger("media_library", "supervisor"))
	libraries.SetConnectionService(connections)
	profiles.SetReferences(libraries)
	profiles.SetRevisionNotifier(libraries)
	storages.SetReferenceChecker(libraries)
	queue := services.NewQueueService(db, audit)
	mediaChanges := services.NewMediaChangeService(db)
	mediaServerRefresh := services.NewMediaServerRefreshService(db, queue, audit, connections)
	mediaChanges.SetReadyHandler(mediaServerRefresh.EnqueueLibrary)
	libraries.SetMediaChangeService(mediaChanges)
	artifacts := services.NewMediaArtifactService(db, queue, signedProxy, logManager.Logger("media_artifact", "worker"))
	artifacts.SetConnectionService(connections)
	artifacts.SetMediaChangeService(mediaChanges)
	libraries.SetArtifactService(artifacts)
	strmManagement := services.NewSTRMManagementService(db, audit, queue, libraries, artifacts, logManager.Logger("strm", "management"))
	artifacts.SetCleanupService(strmManagement)
	providerRegistry := downloadpkg.NewRegistry()
	if err := providerRegistry.Register(models.DownloaderTypeQBittorrent, qbittorrent.Capabilities, func(config downloadpkg.Config) (downloadpkg.Client, error) { return qbittorrent.New(config) }); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "qbittorrent_provider_registration_failed").Msg(logging.OperationServerLifecycle.Message("qBittorrent 驱动注册失败"))
	}
	if err := providerRegistry.Register(models.DownloaderTypePan115Offline, pan115offline.Capabilities, func(config downloadpkg.Config) (downloadpkg.Client, error) { return pan115offline.New(config) }); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "pan115_downloader_registration_failed").Msg(logging.OperationServerLifecycle.Message("115 离线下载驱动注册失败"))
	}
	if cfg.Environment != "production" {
		fakeClient := downloadpkg.NewFakeClient()
		if err := providerRegistry.Register(models.DownloaderTypeFake, downloadpkg.FakeCapabilities, func(downloadpkg.Config) (downloadpkg.Client, error) { return fakeClient, nil }); err != nil {
			logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "fake_downloader_registration_failed").Msg(logging.OperationServerLifecycle.Message("测试下载驱动注册失败"))
		}
	}
	downloaders := services.NewDownloaderService(db, audit, credentialStore, providerRegistry)
	downloaders.SetConnectionService(connections)
	downloadSettings := services.NewDownloadSettingsService(db, audit)
	seedingSettings := services.NewSeedingSettingsService(db, audit)
	metadataSettings := services.NewMetadataSettingsService(db, audit, credentialStore, tmdb.Credential{Kind: tmdb.CredentialKind(cfg.TMDBDeploymentCredentialKind), Value: cfg.TMDBDeploymentCredentialValue})
	aiRecognitionSettings := services.NewAIRecognitionSettingsService(db, audit, credentialStore)
	discoveryService := services.NewDiscoveryService(db, metadataSettings, logManager.Logger("discovery", "service"))
	libraries.SetMetadataSettingsService(metadataSettings)
	libraries.SetAIRecognitionSettings(aiRecognitionSettings)
	artifacts.SetMetadataSettingsService(metadataSettings)
	storages.AddReferenceChecker(downloadSettings)
	downloads := services.NewDownloadService(db, audit, credentialStore, downloaders, downloadSettings, queue, logManager.Logger("download", "service"))
	sites := services.NewSiteService(db, audit, credentialStore, downloads, logManager.Logger("site", "service"))
	sites.SetMetadataSettings(metadataSettings)
	sites.SetAIRecognitionSettings(aiRecognitionSettings)
	cookieCloud := services.NewCookieCloudService(db, audit, credentialStore, sites, logManager.Logger("site", "cookiecloud"))
	downloads.SetMetadataSettings(metadataSettings)
	downloads.SetAIRecognitionSettings(aiRecognitionSettings)
	downloads.SetSeedingSettings(seedingSettings)
	libraries.SetIngestEnqueuer(downloads)
	transfers := services.NewTransferService(db, audit, queue, logManager.Logger("transfer", "service"))
	transfers.SetConnectionService(connections)
	transfers.SetDownloaderService(downloaders)
	transfers.SetMediaChangeService(mediaChanges)
	reorganizations := services.NewMediaReorganizationService(db, audit, queue, metadataSettings, connections, logManager.Logger("media_reorganization", "worker"))
	reorganizations.SetMediaLibraryService(libraries)
	seeding := services.NewSeedingService(db, audit, queue, downloaders, logManager.Logger("seeding", "service"))
	pluginHost := pluginruntime.NewHost(context.Background())
	pluginHostAPI := pluginhostapi.New(db, credentialStore, logManager.Logger("plugin", "host"))
	pluginHost.SetCapabilityHost(pluginHostAPI)
	pluginRepositories := services.NewPluginRepositoryService(db, audit, pluginrepository.NewGitHubClient(nil), logManager.Logger("plugin", "repository"), services.WithPluginRoot(cfg.PluginDirectory), services.WithPluginRuntimeHost(pluginHost), services.WithPluginCredentialStore(credentialStore))
	libraryArtwork := services.NewLibraryArtworkService(db, metadataSettings, pluginRepositories, pluginHostAPI, logManager.Logger("library_artwork", "generator"))
	pluginDownloads := services.NewPluginDownloadExecutor(downloads, pluginRepositories, pluginHostAPI, mediatool.Discover(cfg.FFmpegPath))
	downloads.SetPluginDownloadExecutor(pluginDownloads)
	if err := pluginRepositories.RestorePlugins(context.Background()); err != nil {
		logging.OperationPluginRuntime.Event(log.Fatal()).Str("error_code", services.ErrorCode(err)).Msg(logging.OperationPluginRuntime.Message("插件运行时恢复失败"))
	}
	defer func() {
		if err := pluginRepositories.ClosePlugins(context.Background()); err != nil {
			logging.OperationPluginRuntime.Event(log.Error()).Str("error_code", services.ErrorCode(err)).Msg(logging.OperationPluginRuntime.Message("插件运行时关闭失败"))
		}
	}()
	transfers.SetSeedingService(seeding)
	seeding.SetStagingCleanup(transfers.CleanupAfterSeeding)
	downloads.SetTransferService(transfers)
	api := handlers.NewAPI(cfg, auth, admin, audit, storages, directories, profiles, log)
	api.SetConnectionService(connections)
	api.SetCredentialRevealService(credentialReveal)
	api.SetSignedProxyService(signedProxy)
	api.SetEmbyGatewayService(embyGateway)
	api.SetProviderDirectoryService(providerDirectories)
	api.SetRuntimeLogService(runtimeLogs)
	api.SetMediaLibraryService(libraries)
	api.SetSTRMManagementService(strmManagement)
	api.SetMediaChangeService(mediaChanges)
	api.SetMediaServerRefreshService(mediaServerRefresh)
	queueEvents := services.NewQueueEventHub()
	queue.SetEventHub(queueEvents)
	registry := services.NewWorkerRegistry()
	if cfg.Environment != "production" {
		services.RegisterFakeWorkers(registry)
	}
	if err := registry.Register("download", services.NewDownloadWorker(downloads)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "download_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("下载任务 Worker 注册失败"))
	}
	if err := registry.Register("transfer", services.NewTransferWorker(transfers)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "transfer_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("媒体整理 Worker 注册失败"))
	}
	if err := registry.Register(services.JobTypeMediaReorganization, services.NewMediaReorganizationWorker(reorganizations)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "media_reorganization_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("重新整理 Worker 注册失败"))
	}
	if err := registry.Register("seeding", services.NewSeedingWorker(seeding)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "seeding_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("做种管理 Worker 注册失败"))
	}
	if err := registry.Register(services.JobTypeMediaArtifact, services.NewMediaArtifactWorker(artifacts)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "media_artifact_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("媒体产物 Worker 注册失败"))
	}
	if err := registry.Register(services.JobTypeSTRMReconcile, services.NewSTRMReconcileWorker(strmManagement)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "strm_reconcile_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("STRM 刷新 Worker 注册失败"))
	}
	if err := registry.Register(services.JobTypeMediaServerRefresh, services.NewMediaServerRefreshWorker(mediaServerRefresh)); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "media_server_refresh_worker_registration_failed").Msg(logging.OperationServerLifecycle.Message("媒体服务器刷新 Worker 注册失败"))
	}
	scheduler := services.NewScheduler(queue, registry, logManager.Logger("queue", "scheduler"))
	api.SetQueueService(queue)
	api.SetQueueEventHub(queueEvents)
	api.SetDownloaderService(downloaders)
	api.SetDownloadService(downloads)
	api.SetTransferService(transfers)
	api.SetMediaReorganizationService(reorganizations)
	api.SetDownloadSettingsService(downloadSettings)
	api.SetMetadataSettingsService(metadataSettings)
	api.SetAIRecognitionSettingsService(aiRecognitionSettings)
	api.SetSeedingSettingsService(seedingSettings)
	api.SetSeedingService(seeding)
	api.SetPluginRepositoryService(pluginRepositories)
	api.SetPluginAssetGateway(pluginHostAPI)
	api.SetLibraryArtworkService(libraryArtwork)
	api.SetDiscoveryService(discoveryService)
	api.SetSiteService(sites)
	api.SetCookieCloudService(cookieCloud)
	if err := cookieCloud.Start(context.Background()); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "cookiecloud_start_failed").Msg(logging.OperationServerLifecycle.Message("CookieCloud 同步服务启动失败"))
	}
	defer cookieCloud.Close()
	if err := scheduler.Start(context.Background()); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "scheduler_start_failed").Msg(logging.OperationServerLifecycle.Message("任务调度器启动失败"))
	}
	defer scheduler.Close()
	if err := mediaServerRefresh.RecoverPending(); err != nil {
		logging.OperationServerLifecycle.Event(log.Error()).Err(err).Str("error_code", "media_server_refresh_recovery_failed").Msg(logging.OperationServerLifecycle.Message("媒体服务器刷新恢复失败"))
	}
	if err := libraries.Start(context.Background()); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "media_library_supervisor_start_failed").Msg(logging.OperationServerLifecycle.Message("媒体库监听启动失败"))
	}
	defer libraries.Close()
	providerEvents := services.NewProviderEventService(db, libraries, downloads)
	providerEventMonitor := services.NewProviderEventMonitor(db, connections, providerEvents, logManager.Logger("provider_event", "monitor"))
	if err := providerEventMonitor.Start(context.Background()); err != nil {
		logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "provider_event_monitor_start_failed").Msg(logging.OperationServerLifecycle.Message("115 生活事件监听启动失败"))
	}
	defer providerEventMonitor.Close()
	server := &http.Server{
		Addr: cfg.Address(), Handler: httpserver.New(cfg, api, auth, logManager.Logger("http", "request")),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute,
	}

	go func() {
		logging.OperationServerLifecycle.Event(log.Info()).Str("address", cfg.Address()).Msg(logging.OperationServerLifecycle.Message("OhMyCine Server 已启动"))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.OperationServerLifecycle.Event(log.Fatal()).Err(err).Str("error_code", "server_listen_failed").Msg(logging.OperationServerLifecycle.Message("服务异常停止"))
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	logging.OperationServerLifecycle.Event(log.Info()).Msg(logging.OperationServerLifecycle.Message("OhMyCine Server 已停止"))
}
