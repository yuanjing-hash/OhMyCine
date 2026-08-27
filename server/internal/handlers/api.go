package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type API struct {
	config                config.Config
	auth                  *services.AuthService
	admin                 *services.AdminService
	audit                 *services.AuditService
	connections           *services.ConnectionService
	providerDirectory     *services.ProviderDirectoryService
	storage               *services.StorageService
	directory             *services.DirectoryBrowserService
	profiles              *services.MediaClassificationProfileService
	libraries             *services.MediaLibraryService
	runtimeLogs           *services.RuntimeLogService
	queue                 *services.QueueService
	queueEvents           *services.QueueEventHub
	downloaders           *services.DownloaderService
	downloads             *services.DownloadService
	transfers             *services.TransferService
	reorganizations       *services.MediaReorganizationService
	downloadSettings      *services.DownloadSettingsService
	metadataSettings      *services.MetadataSettingsService
	aiRecognitionSettings *services.AIRecognitionSettingsService
	seedingSettings       *services.SeedingSettingsService
	seeding               *services.SeedingService
	signedProxy           *services.SignedProxyService
	embyGateway           *services.EmbyGatewayService
	strm                  *services.STRMManagementService
	mediaChanges          *services.MediaChangeService
	mediaServerRefresh    *services.MediaServerRefreshService
	pluginRepositories    *services.PluginRepositoryService
	pluginAssets          pluginAssetGateway
	libraryArtwork        *services.LibraryArtworkService
	discovery             *services.DiscoveryService
	mediaCoverage         *services.MediaCoverageService
	follows               *services.FollowService
	sites                 *services.SiteService
	cookieCloud           *services.CookieCloudService
	credentialReveal      *services.CredentialRevealService
	log                   zerolog.Logger
}

func (a *API) SetRuntimeLogService(service *services.RuntimeLogService) { a.runtimeLogs = service }
func (a *API) SetConnectionService(service *services.ConnectionService) { a.connections = service }
func (a *API) SetProviderDirectoryService(service *services.ProviderDirectoryService) {
	a.providerDirectory = service
}
func (a *API) SetMediaLibraryService(service *services.MediaLibraryService) { a.libraries = service }
func (a *API) SetQueueService(service *services.QueueService)               { a.queue = service }
func (a *API) SetQueueEventHub(hub *services.QueueEventHub)                 { a.queueEvents = hub }
func (a *API) SetDownloaderService(service *services.DownloaderService)     { a.downloaders = service }
func (a *API) SetDownloadService(service *services.DownloadService)         { a.downloads = service }
func (a *API) SetTransferService(service *services.TransferService)         { a.transfers = service }
func (a *API) SetMediaReorganizationService(service *services.MediaReorganizationService) {
	a.reorganizations = service
}
func (a *API) SetDownloadSettingsService(service *services.DownloadSettingsService) {
	a.downloadSettings = service
}
func (a *API) SetMetadataSettingsService(service *services.MetadataSettingsService) {
	a.metadataSettings = service
}
func (a *API) SetAIRecognitionSettingsService(service *services.AIRecognitionSettingsService) {
	a.aiRecognitionSettings = service
}
func (a *API) SetSeedingSettingsService(service *services.SeedingSettingsService) {
	a.seedingSettings = service
}
func (a *API) SetSeedingService(service *services.SeedingService)               { a.seeding = service }
func (a *API) SetSignedProxyService(service *services.SignedProxyService)       { a.signedProxy = service }
func (a *API) SetEmbyGatewayService(service *services.EmbyGatewayService)       { a.embyGateway = service }
func (a *API) SetSTRMManagementService(service *services.STRMManagementService) { a.strm = service }
func (a *API) SetMediaChangeService(service *services.MediaChangeService)       { a.mediaChanges = service }
func (a *API) SetMediaServerRefreshService(service *services.MediaServerRefreshService) {
	a.mediaServerRefresh = service
}
func (a *API) SetPluginRepositoryService(service *services.PluginRepositoryService) {
	a.pluginRepositories = service
}
func (a *API) SetPluginAssetGateway(gateway pluginAssetGateway) { a.pluginAssets = gateway }
func (a *API) SetLibraryArtworkService(service *services.LibraryArtworkService) {
	a.libraryArtwork = service
}
func (a *API) SetDiscoveryService(service *services.DiscoveryService) { a.discovery = service }
func (a *API) SetMediaCoverageService(service *services.MediaCoverageService) {
	a.mediaCoverage = service
}
func (a *API) SetFollowService(service *services.FollowService)           { a.follows = service }
func (a *API) SetSiteService(service *services.SiteService)               { a.sites = service }
func (a *API) SetCookieCloudService(service *services.CookieCloudService) { a.cookieCloud = service }
func (a *API) SetCredentialRevealService(service *services.CredentialRevealService) {
	a.credentialReveal = service
}

func NewAPI(cfg config.Config, auth *services.AuthService, admin *services.AdminService, audit *services.AuditService, storage *services.StorageService, directory *services.DirectoryBrowserService, profiles *services.MediaClassificationProfileService, log zerolog.Logger) *API {
	return &API{config: cfg, auth: auth, admin: admin, audit: audit, storage: storage, directory: directory, profiles: profiles, log: log}
}

func (a *API) CookieName() string {
	if a.config.CookieSecure {
		return "__Host-omc_session"
	}
	return "omc_session"
}

func (a *API) Health(c *gin.Context) { success(c, http.StatusOK, gin.H{"status": "ok"}) }

func (a *API) SetupStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	required, recovery, err := a.auth.SetupRequired()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"setup_required": required, "recovery_required": recovery})
}

func (a *API) SetupOwner(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		Username    string `json:"username" binding:"required"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "请填写有效的 owner 信息", Cause: err})
		return
	}
	actor, token, err := a.auth.SetupOwner(services.SetupInput{Username: input.Username, DisplayName: input.DisplayName, Password: input.Password}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	a.setSessionCookie(c, token)
	success(c, http.StatusCreated, gin.H{"user": services.CurrentUserFromActor(actor), "csrf_token": a.auth.CSRFToken(token)})
}

func (a *API) Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "请输入用户名和密码", Cause: err})
		return
	}
	actor, token, err := a.auth.Login(input.Username, input.Password, c.GetHeader("User-Agent"), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	a.setSessionCookie(c, token)
	success(c, http.StatusOK, gin.H{"user": services.CurrentUserFromActor(actor), "csrf_token": a.auth.CSRFToken(token)})
}

func (a *API) Logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	if err := a.auth.Logout(middleware.SessionTokenFrom(c), actor, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	a.clearSessionCookie(c)
	success(c, http.StatusOK, gin.H{"logged_out": true})
}

func (a *API) Me(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	success(c, http.StatusOK, services.CurrentUserFromActor(actor))
}
func (a *API) CSRF(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	success(c, http.StatusOK, gin.H{"csrf_token": a.auth.CSRFToken(middleware.SessionTokenFrom(c))})
}

func (a *API) Dashboard(c *gin.Context) {
	data, err := a.admin.Dashboard()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Permissions(c *gin.Context) {
	data, err := a.admin.ListPermissions()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Users(c *gin.Context) {
	data, err := a.admin.ListUsers()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) CreateUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Username    string `json:"username" binding:"required"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password" binding:"required"`
		RoleIDs     []uint `json:"role_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("用户信息不完整", err))
		return
	}
	data, err := a.admin.CreateUser(actor, services.CreateUserInput{Username: input.Username, DisplayName: input.DisplayName, Password: input.Password, RoleIDs: input.RoleIDs}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		DisplayName *string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("用户信息无效", err))
		return
	}
	data, err := a.admin.UpdateUser(actor, id, services.UpdateUserInput{DisplayName: input.DisplayName}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) SetUserEnabled(c *gin.Context, enabled bool) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.SetUserEnabled(actor, id, enabled, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"enabled": enabled})
}
func (a *API) DeleteUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.DeleteUser(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) AssignRoles(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		RoleIDs []uint `json:"role_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("请选择角色", err))
		return
	}
	if err := a.admin.ReplaceUserRoles(actor, id, input.RoleIDs, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"updated": true})
}

func (a *API) ResetPassword(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Password        string `json:"password" binding:"required"`
		CurrentPassword string `json:"current_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("密码信息无效", err))
		return
	}
	if err := a.admin.ResetPassword(actor, id, input.Password, input.CurrentPassword, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"reset": true})
}

func (a *API) Roles(c *gin.Context) {
	data, err := a.admin.ListRoles()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) CreateRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Code        string   `json:"code" binding:"required"`
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("角色信息不完整", err))
		return
	}
	data, err := a.admin.CreateRole(actor, services.CreateRoleInput{Code: input.Code, Name: input.Name, Description: input.Description, PermissionCodes: input.Permissions}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Active      *bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("角色信息无效", err))
		return
	}
	data, err := a.admin.UpdateRole(actor, id, services.UpdateRoleInput{Name: input.Name, Description: input.Description, Active: input.Active}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) SetRolePermissions(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("权限列表无效", err))
		return
	}
	if err := a.admin.ReplaceRolePermissions(actor, id, input.Permissions, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"updated": true})
}

func (a *API) DeleteRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.DeleteRole(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (a *API) Audit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	data, err := a.audit.List(limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) Storages(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.storage.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) DirectoryRoots(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	data, err := a.directory.Roots(c.Request.Context(), actor, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) Directories(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	token := c.Query("token")
	if token == "" {
		writeError(c, a.log, invalid("缺少目录令牌", nil))
		return
	}
	data, err := a.directory.List(c.Request.Context(), actor, token, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) StorageDirectory(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	data, err := a.directory.StorageToken(c.Request.Context(), actor, id, c.Query("token"), c.Query("page_token"), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) CreateStorage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Name                string `json:"name" binding:"required"`
		Type                string `json:"type"`
		RootPath            string `json:"root_path"`
		PickerToken         string `json:"picker_token"`
		ProviderPickerToken string `json:"provider_picker_token"`
		ConnectionID        *uint  `json:"connection_id"`
		Enabled             *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("存储信息不完整", err))
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.PickerToken != "" {
		root, err := a.directory.ResolveSelection(c.Request.Context(), actor, input.PickerToken)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		input.RootPath = root
	}
	rootDisplayPath := input.RootPath
	if input.ProviderPickerToken != "" {
		if input.ConnectionID == nil {
			writeError(c, a.log, invalid("请选择 115 账号", nil))
			return
		}
		selection, err := a.providerDirectory.ResolveSelection(c.Request.Context(), actor, *input.ConnectionID, input.ProviderPickerToken)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		input.RootPath, rootDisplayPath = selection.ProviderID, selection.DisplayPath
	}
	if input.RootPath == "" {
		writeError(c, a.log, invalid("请选择存储根目录", nil))
		return
	}
	data, err := a.storage.CreateContext(c.Request.Context(), actor, services.StorageInput{Name: input.Name, Type: input.Type, RootPath: input.RootPath, RootDisplayPath: rootDisplayPath, ConnectionID: input.ConnectionID, Enabled: enabled}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateStorage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Name                *string `json:"name"`
		Type                *string `json:"type"`
		RootPath            *string `json:"root_path"`
		PickerToken         *string `json:"picker_token"`
		ProviderPickerToken *string `json:"provider_picker_token"`
		ConnectionID        *uint   `json:"connection_id"`
		Enabled             *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("存储信息无效", err))
		return
	}
	if input.PickerToken != nil {
		root, err := a.directory.ResolveSelection(c.Request.Context(), actor, *input.PickerToken)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		input.RootPath = &root
	}
	var rootDisplayPath *string
	if input.ProviderPickerToken != nil {
		if input.ConnectionID == nil {
			writeError(c, a.log, invalid("请选择 115 账号", nil))
			return
		}
		selection, err := a.providerDirectory.ResolveSelection(c.Request.Context(), actor, *input.ConnectionID, *input.ProviderPickerToken)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		input.RootPath, rootDisplayPath = &selection.ProviderID, &selection.DisplayPath
	}
	data, err := a.storage.UpdateContext(c.Request.Context(), actor, id, services.UpdateStorageInput{Name: input.Name, Type: input.Type, RootPath: input.RootPath, RootDisplayPath: rootDisplayPath, ConnectionID: input.ConnectionID, Enabled: input.Enabled}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) TestStorage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	data, err := a.storage.TestContext(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) DeleteStorage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.storage.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) MediaClassificationProfiles(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.profiles.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) MediaClassificationProfile(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	data, err := a.profiles.Get(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) CreateMediaClassificationProfile(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Name                    string           `json:"name" binding:"required"`
		Rules                   json.RawMessage  `json:"rules"`
		BuiltinRecognitionPacks *[]string        `json:"builtin_recognition_packs"`
		RecognitionRules        *json.RawMessage `json:"recognition_rules"`
		MovieDirectoryTemplate  *string          `json:"movie_directory_template"`
		MovieFilenameTemplate   *string          `json:"movie_filename_template"`
		TVDirectoryTemplate     *string          `json:"tv_directory_template"`
		TVFilenameTemplate      *string          `json:"tv_filename_template"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("媒体分类规则信息无效", err))
		return
	}
	data, err := a.profiles.Create(actor, services.CreateMediaClassificationProfileInput{Name: input.Name, Rules: input.Rules, BuiltinRecognitionPacks: input.BuiltinRecognitionPacks, RecognitionRules: input.RecognitionRules, MovieDirectoryTemplate: input.MovieDirectoryTemplate, MovieFilenameTemplate: input.MovieFilenameTemplate, TVDirectoryTemplate: input.TVDirectoryTemplate, TVFilenameTemplate: input.TVFilenameTemplate}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) CopyMediaClassificationProfile(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Name *string `json:"name"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("副本信息无效", err))
		return
	}
	data, err := a.profiles.Copy(actor, id, services.CopyMediaClassificationProfileInput{Name: input.Name}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateMediaClassificationProfile(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Revision                uint64           `json:"revision" binding:"required"`
		Name                    string           `json:"name" binding:"required"`
		Rules                   json.RawMessage  `json:"rules" binding:"required"`
		BuiltinRecognitionPacks *[]string        `json:"builtin_recognition_packs"`
		RecognitionRules        *json.RawMessage `json:"recognition_rules"`
		MovieDirectoryTemplate  *string          `json:"movie_directory_template"`
		MovieFilenameTemplate   *string          `json:"movie_filename_template"`
		TVDirectoryTemplate     *string          `json:"tv_directory_template"`
		TVFilenameTemplate      *string          `json:"tv_filename_template"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("媒体分类规则信息无效", err))
		return
	}
	data, err := a.profiles.Update(actor, id, services.UpdateMediaClassificationProfileInput{Revision: input.Revision, Name: input.Name, Rules: input.Rules, BuiltinRecognitionPacks: input.BuiltinRecognitionPacks, RecognitionRules: input.RecognitionRules, MovieDirectoryTemplate: input.MovieDirectoryTemplate, MovieFilenameTemplate: input.MovieFilenameTemplate, TVDirectoryTemplate: input.TVDirectoryTemplate, TVFilenameTemplate: input.TVFilenameTemplate}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func strictJSON(c *gin.Context, target any) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (a *API) DeleteMediaClassificationProfile(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.profiles.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) setSessionCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: a.CookieName(), Value: token, Path: "/", MaxAge: int(a.config.SessionMaxTTL.Seconds()), HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (a *API) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: a.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func pathID(c *gin.Context) (uint, bool) {
	value, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || value == 0 {
		writeError(c, zerolog.Nop(), invalid("资源 ID 无效", err))
		return 0, false
	}
	return uint(value), true
}
func invalid(message string, err error) error {
	return &services.AppError{Code: services.CodeInvalidRequest, Message: message, Cause: err}
}
