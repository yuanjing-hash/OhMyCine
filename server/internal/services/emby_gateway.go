package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver/emby"
	"gorm.io/gorm"
)

const (
	embyPlaybackBodyLimit              = 2 << 20
	embyWebCompatibilityBodyLimit      = 4 << 20
	embyWebCompatibilityAssetPath      = "/web/ohmycine-directplay.js"
	embyWebCompatibilityMarker         = "data-ohmycine-directplay"
	embyTicketTTL                      = 10 * time.Minute
	embyTicketMaximum                  = 8192
	playbackTicketParam                = "omc_ticket"
	embyAliasMinLength                 = 3
	embyAliasMaxLength                 = 32
	embyWebCompatibilityScriptTemplate = `(()=>{"use strict";
const options={externalPlayer:__EXTERNAL_PLAYER__,fanart:__FANART__};
const mediaPrototype=globalThis.HTMLMediaElement&&HTMLMediaElement.prototype;
if(mediaPrototype){try{Object.defineProperty(mediaPrototype,"crossOrigin",{get:function(){return null},set:function(){},configurable:false})}catch(_){}}
function clearCrossOrigin(node){if(!node||node.nodeType!==1)return;if(node instanceof HTMLMediaElement)node.removeAttribute("crossorigin");if(node.querySelectorAll)node.querySelectorAll("video[crossorigin],audio[crossorigin]").forEach(function(element){element.removeAttribute("crossorigin")})}
function protectDirectPlay(){if(!document.documentElement)return;clearCrossOrigin(document.documentElement);new MutationObserver(function(mutations){mutations.forEach(function(mutation){if(mutation.type==="attributes")clearCrossOrigin(mutation.target);else mutation.addedNodes.forEach(clearCrossOrigin)})}).observe(document.documentElement,{attributes:true,attributeFilter:["crossorigin"],childList:true,subtree:true})}
document.documentElement?protectDirectPlay():document.addEventListener("DOMContentLoaded",protectDirectPlay,{once:true});
if(!options.externalPlayer&&!options.fanart)return;
const ids={players:"ohmycine-external-players",fanart:"ohmycine-fanart",style:"ohmycine-emby-enhancements",viewer:"ohmycine-fanart-viewer"};
let scheduled=false,lastPageID="",eventItem=null,externalLoading=false;
function addStyles(){if(document.getElementById(ids.style))return;const style=document.createElement("style");style.id=ids.style;style.textContent="#"+ids.players+"{display:flex;flex-wrap:wrap;gap:.65em;margin:.75em 0 1.25em}#"+ids.players+" .ohmycine-player-button{min-width:7.2em}#"+ids.players+" .ohmycine-player-status{align-self:center;opacity:.72;font-size:.9em}#"+ids.fanart+"{margin:1.8em 0}#"+ids.fanart+" .ohmycine-fanart-list{display:flex;gap:1em;overflow-x:auto;scroll-snap-type:x proximity;padding:.25em .25em 1em}#"+ids.fanart+" .ohmycine-fanart-card{appearance:none;border:0;background:transparent;padding:0;cursor:pointer;flex:0 0 clamp(16em,28vw,30em);scroll-snap-align:start;border-radius:.45em;overflow:hidden}#"+ids.fanart+" img{display:block;width:100%;aspect-ratio:16/9;object-fit:cover;background:#222}#"+ids.viewer+"{position:fixed;inset:0;z-index:2147483646;background:rgba(0,0,0,.9);display:flex;align-items:center;justify-content:center;padding:3vw}#"+ids.viewer+"[hidden]{display:none}#"+ids.viewer+" img{max-width:96vw;max-height:92vh;object-fit:contain}#"+ids.viewer+" button{position:absolute;right:1.25em;top:1.25em;border:0;border-radius:50%;width:2.75em;height:2.75em;background:rgba(255,255,255,.16);color:#fff;font-size:1.4em;cursor:pointer}";document.head.appendChild(style)}
function pageID(){try{const match=(location.hash||"").match(/[?&]id=([^&]+)/i);return match?decodeURIComponent(match[1]):""}catch(_){return ""}}
function userID(){try{return ApiClient.getCurrentUserId?ApiClient.getCurrentUserId():(ApiClient._serverInfo&&ApiClient._serverInfo.UserId)||""}catch(_){return ""}}
async function getDetailItem(id){if(eventItem&&String(eventItem.Id)===String(id))return eventItem;return ApiClient.getItem(userID(),id)}
async function getPlayableItem(id){let item=await getDetailItem(id);if(item&&Array.isArray(item.MediaSources)&&item.MediaSources.length)return item;if(item&&item.Type==="Series"&&ApiClient.getNextUpEpisodes){const next=await ApiClient.getNextUpEpisodes({SeriesId:item.Id,UserId:userID()});if(next&&next.Items&&next.Items.length)return ApiClient.getItem(userID(),next.Items[0].Id)}if(item&&ApiClient.getItems){const children=await ApiClient.getItems(userID(),{parentId:item.Id,Recursive:true,IsFolder:false,Limit:1});if(children&&children.Items&&children.Items.length)return ApiClient.getItem(userID(),children.Items[0].Id)}return null}
function selectedSourceID(){const select=document.querySelector("div[is='emby-scroller']:not(.hide) select.selectSource:not([disabled])");return select&&select.value?String(select.value):""}
function safeGatewayStream(source){if(!source)return"";const direct=String(source.DirectStreamUrl||source.Path||"");if(!direct||direct.charAt(0)!=="/")return"";try{const server=String(ApiClient.serverAddress()).replace(/\/$/,"");const value=new URL(server+"/emby"+direct,location.href);const serverURL=new URL(server,location.href);if(value.origin!==location.origin||serverURL.origin!==location.origin)return"";if(value.searchParams.getAll("omc_ticket").length!==1||!value.searchParams.get("omc_ticket"))return"";if(value.searchParams.getAll("MediaSourceId").length!==1)return"";const prefix=serverURL.pathname.replace(/\/$/,"")+"/emby/";if(value.pathname.indexOf(prefix)!==0)return"";return value.href}catch(_){return""}}
async function externalStream(id){const item=await getPlayableItem(id);if(!item)return null;const playback=await ApiClient.getPlaybackInfo(item.Id,{},{});const sources=playback&&Array.isArray(playback.MediaSources)?playback.MediaSources:[];const wanted=selectedSourceID();let source=null;if(wanted)source=sources.find(function(candidate){return String(candidate.Id)===wanted&&safeGatewayStream(candidate)});if(!source)source=sources.find(function(candidate){return !!safeGatewayStream(candidate)});const stream=safeGatewayStream(source);return stream?{url:stream,title:String(item.Name||source.Name||"OhMyCine")} : null}
function base64URL(value){const bytes=new TextEncoder().encode(value);let binary="";bytes.forEach(function(byte){binary+=String.fromCharCode(byte)});return btoa(binary).replace(/\+/g,"-").replace(/\//g,"_").replace(/=+$/g,"")}
function platformPlayers(){const ua=navigator.userAgent||"";if(/Android/i.test(ua))return[["VLC","vlc"],["MPV","mpv"],["MX Player","mx"]];if(/iPad|iPhone|iPod/i.test(ua))return[["VLC","vlc"],["Infuse","infuse"]];if(/Macintosh|MacIntel/i.test(ua))return[["IINA","iina"],["VLC","vlc"],["MPV","mpv"],["Infuse","infuse"]];if(/Windows/i.test(ua))return[["PotPlayer","potplayer"],["VLC","vlc"],["MPV","mpv"],["弹弹Play","ddplay"]];return[["VLC","vlc"],["MPV","mpv"]]}
function protocolURL(kind,stream,title){const ua=navigator.userAgent||"";if(kind==="potplayer")return"potplayer://"+encodeURI(stream);if(kind==="iina")return"iina://weblink?url="+encodeURIComponent(stream)+"&new_window=1";if(kind==="infuse")return"infuse://x-callback-url/play?url="+encodeURIComponent(stream);if(kind==="ddplay")return"ddplay:"+encodeURIComponent(stream+"|filePath="+title);if(kind==="mx")return"intent:"+encodeURI(stream)+"#Intent;package=com.mxtech.videoplayer.ad;S.title="+encodeURIComponent(title)+";end";if(kind==="vlc"){if(/Android/i.test(ua))return"intent:"+encodeURI(stream)+"#Intent;package=org.videolan.vlc;type=video/*;S.title="+encodeURIComponent(title)+";end";if(/iPad|iPhone|iPod/i.test(ua))return"vlc-x-callback://x-callback-url/stream?url="+encodeURIComponent(stream);return"vlc://"+encodeURI(stream)}if(kind==="mpv"){if(/Android|iPad|iPhone|iPod/i.test(ua))return"mpv-handler://"+encodeURI(stream);return"mpv-handler://play/"+base64URL(stream)}return""}
function playerButton(label,kind,id,status){const button=document.createElement("button");button.type="button";button.className="detailButton emby-button emby-button-backdropfilter raised-backdropfilter detailButton-primary ohmycine-player-button";button.title="使用 "+label+" 打开";const content=document.createElement("div");content.className="detailButton-content";const icon=document.createElement("i");icon.className="md-icon detailButton-icon button-icon button-icon-left";icon.textContent="open_in_new";const text=document.createElement("span");text.className="button-text";text.textContent=label;content.appendChild(icon);content.appendChild(text);button.appendChild(content);button.addEventListener("click",async function(){if(button.disabled)return;button.disabled=true;status.textContent="正在获取安全播放地址…";try{const media=await externalStream(id);if(!media)throw new Error("unavailable");const target=protocolURL(kind,media.url,media.title);if(!target)throw new Error("unsupported");status.textContent="已请求打开 "+label;window.location.href=target}catch(_){status.textContent="当前媒体不是可安全外部播放的 OhMyCine STRM"}finally{button.disabled=false}});return button}
async function injectExternal(id){if(!options.externalPlayer||externalLoading||document.getElementById(ids.players))return;const anchor=document.querySelector("div[is='emby-scroller']:not(.hide) .mainDetailButtons");if(!anchor)return;externalLoading=true;try{const initial=await externalStream(id);if(!initial||id!==pageID())return;const wrapper=document.createElement("div");wrapper.id=ids.players;wrapper.className="detailButtons flex align-items-flex-start flex-wrap-wrap detail-lineItem";const status=document.createElement("span");status.className="ohmycine-player-status";status.setAttribute("aria-live","polite");platformPlayers().forEach(function(player){wrapper.appendChild(playerButton(player[0],player[1],id,status))});wrapper.appendChild(status);anchor.insertAdjacentElement("afterend",wrapper)}catch(_){}finally{externalLoading=false}}
function ensureViewer(){let viewer=document.getElementById(ids.viewer);if(viewer)return viewer;viewer=document.createElement("div");viewer.id=ids.viewer;viewer.hidden=true;viewer.setAttribute("role","dialog");viewer.setAttribute("aria-label","同人图预览");const image=document.createElement("img");image.alt="";const close=document.createElement("button");close.type="button";close.textContent="×";close.setAttribute("aria-label","关闭预览");function hide(){viewer.hidden=true;image.removeAttribute("src")}close.addEventListener("click",hide);viewer.addEventListener("click",function(event){if(event.target===viewer)hide()});document.addEventListener("keydown",function(event){if(event.key==="Escape"&&!viewer.hidden)hide()});viewer.appendChild(image);viewer.appendChild(close);document.body.appendChild(viewer);return viewer}
function showFanart(url){const viewer=ensureViewer();const image=viewer.querySelector("img");image.src=url;viewer.hidden=false}
async function injectFanart(id){if(!options.fanart||document.getElementById(ids.fanart))return;const host=document.querySelector(".itemView:not(.hide) .details-additionalContent")||document.querySelector("div[is='emby-scroller']:not(.hide) .details-additionalContent");if(!host)return;try{const item=await getDetailItem(id);if(!item||["Movie","Series","Person","Video"].indexOf(item.Type)<0||!Array.isArray(item.BackdropImageTags)||!item.BackdropImageTags.length||id!==pageID())return;const section=document.createElement("section");section.id=ids.fanart;section.className="verticalSection";const heading=document.createElement("h2");heading.className="sectionTitle sectionTitle-cards padded-left";heading.textContent="同人图 / 剧照";const list=document.createElement("div");list.className="ohmycine-fanart-list padded-left padded-right";const seen=new Set();item.BackdropImageTags.slice(0,30).forEach(function(tag,index){if(!tag||seen.has(tag))return;seen.add(tag);const imageURL=ApiClient.getImageUrl(item.Id,{type:"Backdrop",index:index,tag:tag});const card=document.createElement("button");card.type="button";card.className="ohmycine-fanart-card";card.setAttribute("aria-label","查看同人图 "+String(index+1));const image=document.createElement("img");image.loading="lazy";image.alt=String(item.Name||"")+" 同人图 "+String(index+1);image.src=imageURL;card.appendChild(image);card.addEventListener("click",function(){showFanart(imageURL)});list.appendChild(card)});if(!list.childElementCount)return;section.appendChild(heading);section.appendChild(list);host.appendChild(section)}catch(_){}}
function refresh(){scheduled=false;if(typeof ApiClient==="undefined")return;const id=pageID();if(!id)return;if(id!==lastPageID){lastPageID=id;eventItem=null;const oldPlayers=document.getElementById(ids.players);if(oldPlayers)oldPlayers.remove();const oldFanart=document.getElementById(ids.fanart);if(oldFanart)oldFanart.remove()}addStyles();injectExternal(id);injectFanart(id)}
function schedule(){if(scheduled)return;scheduled=true;setTimeout(refresh,180)}
document.addEventListener("viewbeforeshow",function(event){eventItem=event&&event.target&&event.target.controller?event.target.controller.currentItem:null;schedule()});window.addEventListener("hashchange",schedule);const start=function(){addStyles();new MutationObserver(schedule).observe(document.body,{childList:true,subtree:true});schedule()};document.body?start():document.addEventListener("DOMContentLoaded",start,{once:true});
})();`
)

var (
	embyAliasPattern                 = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	embyBasePlayerCrossOriginPattern = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*\.IsRemote\s*&&\s*["']DirectPlay["']\s*===\s*[A-Za-z_$][A-Za-z0-9_$]*\s*\?\s*null\s*:\s*["']anonymous["']`)
	embyPluginCrossOriginPattern     = regexp.MustCompile(`&&\(\s*[A-Za-z_$][A-Za-z0-9_$]*\.crossOrigin\s*=\s*[A-Za-z_$][A-Za-z0-9_$]*\s*\)`)
	embyHTMLHeadPattern              = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)
	embyAliasReserved                = map[string]struct{}{
		"admin": {}, "api": {}, "assets": {}, "emby": {}, "health": {}, "login": {}, "logout": {},
		"proxy": {}, "setup": {}, "socket": {}, "strm": {}, "system": {}, "web": {}, "websocket": {},
	}
)

type embyWebPatchKind uint8

const (
	embyWebPatchNone embyWebPatchKind = iota
	embyWebPatchBasePlayer
	embyWebPatchPlugin
	embyWebPatchIndex
)

type playbackGrant struct {
	GatewayID      string
	PolicyRevision uint64
	ItemID         string
	MediaSource    string
	Artifact       string
	ExpiresAt      time.Time
}

type embyGatewayTarget struct {
	GatewayID             uint
	ConnectionID          uint
	PublicID              string
	GatewayEnabled        bool
	ExternalPlayerEnabled bool
	FanartEnabled         bool
	PolicyRevision        uint64
	Endpoint              string
	Provider              string
	ConnectionEnabled     bool
	HealthStatus          string
}

type EmbyGatewaySummary struct {
	ConnectionID          uint   `json:"connection_id"`
	PublicID              string `json:"public_id"`
	Alias                 string `json:"alias"`
	Enabled               bool   `json:"enabled"`
	ExternalPlayerEnabled bool   `json:"external_player_enabled"`
	FanartEnabled         bool   `json:"fanart_enabled"`
	BaseURL               string `json:"base_url"`
	Revision              uint64 `json:"revision"`
}

type EmbyGatewaySettingsInput struct {
	Enabled               bool
	Alias                 *string
	ExternalPlayerEnabled *bool
	FanartEnabled         *bool
	Revision              uint64
}

// EmbyGatewayService is an Emby protocol adapter above the signed STRM
// resolver. It never decrypts or injects the Server-side Emby API key.
type EmbyGatewayService struct {
	db           *gorm.DB
	audit        *AuditService
	signedProxy  *SignedProxyService
	publicOrigin string
	log          zerolog.Logger
	now          func() time.Time
	secret       []byte
	transport    http.RoundTripper

	mu     sync.Mutex
	grants map[string]playbackGrant
}

func NewEmbyGatewayService(db *gorm.DB, audit *AuditService, signedProxy *SignedProxyService, publicOrigin string, log zerolog.Logger) (*EmbyGatewayService, error) {
	if db == nil || audit == nil || signedProxy == nil {
		return nil, errors.New("emby gateway dependencies are unavailable")
	}
	parsed, err := emby.ParseEndpoint(publicOrigin)
	if err != nil || parsed.Path != "" || wildcardPublicOrigin(parsed) {
		return nil, errors.New("emby gateway public origin is invalid")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &EmbyGatewayService{db: db, audit: audit, signedProxy: signedProxy, publicOrigin: strings.TrimRight(publicOrigin, "/"), log: log, now: func() time.Time { return time.Now().UTC() }, secret: secret, transport: transport, grants: map[string]playbackGrant{}}, nil
}

func (s *EmbyGatewayService) Get(actor Actor, connectionID uint) (EmbyGatewaySummary, error) {
	if !actor.Can(authz.PermissionConnectionsRead) {
		return EmbyGatewaySummary{}, appError(CodePermissionDenied, "无权查看 Emby 网关", nil)
	}
	target, err := s.gatewayByConnection(connectionID)
	if err != nil {
		return EmbyGatewaySummary{}, err
	}
	return s.summary(target), nil
}

func (s *EmbyGatewayService) Configure(actor Actor, connectionID uint, enabled bool, revision uint64, request RequestContext) (EmbyGatewaySummary, error) {
	return s.ConfigureSettings(actor, connectionID, EmbyGatewaySettingsInput{Enabled: enabled, Revision: revision}, request)
}

func (s *EmbyGatewayService) ConfigureSettings(actor Actor, connectionID uint, input EmbyGatewaySettingsInput, request RequestContext) (EmbyGatewaySummary, error) {
	if !actor.Can(authz.PermissionConnectionsUpdate) {
		return EmbyGatewaySummary{}, appError(CodePermissionDenied, "无权配置 Emby 网关", nil)
	}
	target, err := s.gatewayByConnection(connectionID)
	if err != nil {
		return EmbyGatewaySummary{}, err
	}
	if input.Revision == 0 || input.Revision != target.PolicyRevision {
		return EmbyGatewaySummary{}, appError(CodeConflict, "Emby 网关配置已变化，请刷新后重试", nil)
	}
	if input.Enabled && (!target.ConnectionEnabled || target.HealthStatus != "online") {
		return EmbyGatewaySummary{}, appError(CodeEmbyGatewayUnavailable, "请先成功测试已启用的 Emby 连接", nil)
	}
	alias := target.PublicID
	aliasChanged := false
	if input.Alias != nil {
		var normalizeErr error
		alias, normalizeErr = normalizeEmbyGatewayAlias(*input.Alias)
		if normalizeErr != nil {
			return EmbyGatewaySummary{}, normalizeErr
		}
		aliasChanged = alias != target.PublicID
	}
	externalPlayerEnabled := target.ExternalPlayerEnabled
	if input.ExternalPlayerEnabled != nil {
		externalPlayerEnabled = *input.ExternalPlayerEnabled
	}
	fanartEnabled := target.FanartEnabled
	if input.FanartEnabled != nil {
		fanartEnabled = *input.FanartEnabled
	}
	next := input.Revision + 1
	now := s.now()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if aliasChanged {
			var duplicate int64
			if err := tx.Model(&models.EmbyProxyGateway{}).Where("id <> ? AND lower(public_id) = ?", target.GatewayID, alias).Count(&duplicate).Error; err != nil {
				return err
			}
			if duplicate != 0 {
				return appError(CodeEmbyGatewayAliasConflict, "Emby 网关别名已被使用", nil)
			}
		}
		result := tx.Model(&models.EmbyProxyGateway{}).Where("id = ? AND policy_revision = ?", target.GatewayID, input.Revision).Updates(map[string]any{"public_id": alias, "enabled": input.Enabled, "external_player_enabled": externalPlayerEnabled, "fanart_enabled": fanartEnabled, "policy_revision": next, "updated_at": now})
		if result.Error != nil {
			if isUniqueConstraint(result.Error) {
				return appError(CodeEmbyGatewayAliasConflict, "Emby 网关别名已被使用", result.Error)
			}
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "Emby 网关配置已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "emby_gateway.update", "connection", uintID(connectionID), "success", map[string]any{"enabled": input.Enabled, "alias_changed": aliasChanged, "external_player_enabled": externalPlayerEnabled, "fanart_enabled": fanartEnabled}, request)
	})
	if err != nil {
		return EmbyGatewaySummary{}, err
	}
	target.PublicID, target.GatewayEnabled, target.ExternalPlayerEnabled, target.FanartEnabled, target.PolicyRevision = alias, input.Enabled, externalPlayerEnabled, fanartEnabled, next
	return s.summary(target), nil
}

func (s *EmbyGatewayService) ServeHTTP(w http.ResponseWriter, r *http.Request, publicID, gatewayPath string) {
	target, err := s.gatewayByPublicID(publicID)
	if err != nil || !target.GatewayEnabled || !target.ConnectionEnabled || target.HealthStatus != "online" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}
	upstream, err := emby.ParseEndpoint(target.Endpoint)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	gatewayPath, ok := safeGatewayPath(gatewayPath, r.URL.RawPath)
	if !ok {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if serveEmbyWebCompatibilityAsset(w, r, gatewayPath, target) {
		return
	}
	controller := http.NewResponseController(w)
	// Emby image/file responses may legitimately stream longer than the
	// Server's global WriteTimeout. WebSocket upgrades additionally need the
	// accepted connection's inherited read deadline cleared before hijacking.
	_ = controller.SetWriteDeadline(time.Time{})
	if isWebSocketUpgrade(r.Header) {
		_ = controller.SetReadDeadline(time.Time{})
	}
	itemID, playback := playbackInfoItem(gatewayPath)
	webPatch := embyWebPlayerPatch(gatewayPath, r.Method)
	query := r.URL.Query()
	mediaItem, mediaRoute := mediaRouteBinding(gatewayPath)
	ticket, ticketPresent, ticketUnique := singleQueryFold(query, playbackTicketParam)
	if !ticketPresent && rawQueryHasFold(r.URL.RawQuery, playbackTicketParam) {
		ticketPresent, ticketUnique = true, false
	}
	if ticketPresent {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !ticketUnique || !mediaRoute || strings.TrimSpace(ticket) == "" {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		mediaSource, sourcePresent, sourceUnique := singleQueryFold(query, "MediaSourceId")
		if !sourcePresent || !sourceUnique || strings.TrimSpace(mediaSource) == "" {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		grant, ticketErr := s.verifyTicket(strings.TrimSpace(ticket), publicID, mediaItem, strings.TrimSpace(mediaSource))
		if ticketErr != nil {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		redirect, resolveErr := s.signedProxy.ResolveArtifactForClient(r.Context(), grant.Artifact, r.Header.Get("User-Agent"), r.RemoteAddr)
		if resolveErr != nil {
			serverlog.OperationEmbyProxyGateway.Event(s.log.Warn()).
				Uint("gateway_id", target.GatewayID).
				Str("error_code", ErrorCode(resolveErr)).
				Msg(serverlog.OperationEmbyProxyGateway.Message("播放直链解析失败"))
			http.Error(w, http.StatusText(ProxyHTTPStatus(resolveErr)), ProxyHTTPStatus(resolveErr))
			return
		}
		w.Header().Set("Location", redirect.URL)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusFound)
		return
	}
	if playback {
		if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, POST, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if !s.boundPlaybackRequest(w, r) {
			return
		}
	}

	request := r.Clone(context.WithValue(r.Context(), embyProxyContextKey{}, embyProxyContext{Gateway: target, ItemID: itemID, Playback: playback, WebPatch: webPatch}))
	request.URL.Path = gatewayPath
	request.URL.RawPath = ""
	proxy := &httputil.ReverseProxy{
		Transport: s.transport,
		Rewrite: func(out *httputil.ProxyRequest) {
			out.Out.URL.Scheme = upstream.Scheme
			out.Out.URL.Host = upstream.Host
			out.Out.URL.Path = joinGatewayPath(upstream.Path, out.In.URL.Path)
			out.Out.URL.RawPath = ""
			out.Out.URL.RawQuery = out.In.URL.RawQuery
			out.Out.Host = upstream.Host
			out.Out.Header.Del("Proxy-Authorization")
			out.Out.Header.Del("Forwarded")
			out.Out.Header.Del("X-Forwarded-For")
			out.Out.Header.Del("X-Forwarded-Host")
			out.Out.Header.Del("X-Forwarded-Proto")
			if playback {
				out.Out.Header.Del("Accept-Encoding")
			}
			if webPatch != embyWebPatchNone {
				// A cached unmodified Emby Web asset would keep setting
				// crossOrigin=anonymous and make the browser enforce CORS on the
				// final 115 CDN response. Force a fresh bounded asset through the
				// compatibility patch instead of accepting an upstream 304.
				out.Out.Header.Set("Accept-Encoding", "identity")
				out.Out.Header.Del("If-Match")
				out.Out.Header.Del("If-Modified-Since")
				out.Out.Header.Del("If-None-Match")
				out.Out.Header.Del("If-Range")
				out.Out.Header.Del("If-Unmodified-Since")
			}
		},
		ModifyResponse: s.modifyResponse,
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, request)
}

type embyProxyContextKey struct{}
type embyProxyContext struct {
	Gateway  embyGatewayTarget
	ItemID   string
	Playback bool
	WebPatch embyWebPatchKind
}

func (s *EmbyGatewayService) modifyResponse(response *http.Response) error {
	ctx, _ := response.Request.Context().Value(embyProxyContextKey{}).(embyProxyContext)
	if location := strings.TrimSpace(response.Header.Get("Location")); location != "" {
		parsed, err := url.Parse(location)
		if err != nil || parsed.User != nil || (parsed.IsAbs() && parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("emby redirect is invalid")
		}
		if rewritten, ok := s.rewriteLocation(ctx.Gateway, response.Request.URL, location); ok {
			response.Header.Set("Location", rewritten)
		}
	}
	if ctx.WebPatch != embyWebPatchNone {
		return s.modifyWebCompatibilityResponse(response, ctx)
	}
	if !ctx.Playback || response.Request.Method == http.MethodHead || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, embyPlaybackBodyLimit+1))
	_ = response.Body.Close()
	if err != nil || len(body) > embyPlaybackBodyLimit {
		return errors.New("emby playback response exceeds limit")
	}
	rewritten, count, err := s.rewritePlaybackInfo(ctx.Gateway, ctx.ItemID, body)
	if err != nil {
		return err
	}
	if count == 0 {
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.ContentLength = int64(len(body))
		return nil
	}
	response.Body = io.NopCloser(bytes.NewReader(rewritten))
	response.ContentLength = int64(len(rewritten))
	response.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Del("Content-Encoding")
	response.Header.Del("ETag")
	response.Header.Del("Content-MD5")
	response.Header.Del("Last-Modified")
	serverlog.OperationEmbyProxyGateway.Event(s.log.Info()).Uint("gateway_id", ctx.Gateway.GatewayID).Int("source_count", count).Msg(serverlog.OperationEmbyProxyGateway.Message("PlaybackInfo 已接管"))
	return nil
}

func (s *EmbyGatewayService) modifyWebCompatibilityResponse(response *http.Response, ctx embyProxyContext) error {
	if response.Request.Method != http.MethodGet || response.StatusCode != http.StatusOK {
		return nil
	}
	contentType := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Type")))
	if ctx.WebPatch == embyWebPatchIndex {
		if !strings.Contains(contentType, "text/html") {
			return nil
		}
	} else if !strings.Contains(contentType, "javascript") {
		return nil
	}
	if strings.TrimSpace(response.Header.Get("Content-Encoding")) != "" {
		return errors.New("emby web player asset is unexpectedly encoded")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, embyWebCompatibilityBodyLimit+1))
	_ = response.Body.Close()
	if err != nil || len(body) > embyWebCompatibilityBodyLimit {
		return errors.New("emby web compatibility response exceeds limit")
	}
	assetURL := "/emby/" + url.PathEscape(ctx.Gateway.PublicID) + embyWebCompatibilityAssetPath
	patched, changed := patchEmbyWebCompatibilityResponse(ctx.WebPatch, body, assetURL)
	response.Body = io.NopCloser(bytes.NewReader(patched))
	response.ContentLength = int64(len(patched))
	response.Header.Set("Content-Length", strconv.Itoa(len(patched)))
	response.Header.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	response.Header.Set("Pragma", "no-cache")
	response.Header.Set("Expires", "0")
	response.Header.Del("Accept-Ranges")
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-MD5")
	response.Header.Del("ETag")
	response.Header.Del("Last-Modified")
	response.Header.Del("SourceMap")
	response.Header.Del("X-SourceMap")
	if changed {
		serverlog.OperationEmbyProxyGateway.Event(s.log.Info()).
			Uint("gateway_id", ctx.Gateway.GatewayID).
			Str("asset", ctx.WebPatch.String()).
			Msg(serverlog.OperationEmbyProxyGateway.Message("Emby Web 直链兼容补丁已应用"))
	} else {
		serverlog.OperationEmbyProxyGateway.Event(s.log.Warn()).
			Uint("gateway_id", ctx.Gateway.GatewayID).
			Str("asset", ctx.WebPatch.String()).
			Str("error_code", "emby_web_patch_pattern_missing").
			Msg(serverlog.OperationEmbyProxyGateway.Message("Emby Web 直链兼容补丁未匹配"))
	}
	return nil
}

func patchEmbyWebCompatibilityResponse(kind embyWebPatchKind, body []byte, assetURL string) ([]byte, bool) {
	var patched []byte
	switch kind {
	case embyWebPatchBasePlayer:
		patched = embyBasePlayerCrossOriginPattern.ReplaceAll(body, []byte("null"))
	case embyWebPatchPlugin:
		patched = embyPluginCrossOriginPattern.ReplaceAll(body, nil)
	case embyWebPatchIndex:
		if bytes.Contains(body, []byte(embyWebCompatibilityMarker)) {
			return body, true
		}
		head := embyHTMLHeadPattern.FindIndex(body)
		if head == nil {
			return body, false
		}
		tag := []byte(`<script src="` + html.EscapeString(assetURL) + `" ` + embyWebCompatibilityMarker + `></script>`)
		patched = make([]byte, 0, len(body)+len(tag))
		patched = append(patched, body[:head[1]]...)
		patched = append(patched, tag...)
		patched = append(patched, body[head[1]:]...)
	default:
		return body, false
	}
	return patched, !bytes.Equal(body, patched)
}

func embyWebPlayerPatch(path, method string) embyWebPatchKind {
	if method != http.MethodGet {
		return embyWebPatchNone
	}
	path = strings.ToLower(normalizeGatewayPath(path))
	if strings.HasPrefix(path, "/emby/") {
		path = strings.TrimPrefix(path, "/emby")
	}
	switch path {
	case "/web/modules/htmlvideoplayer/basehtmlplayer.js":
		return embyWebPatchBasePlayer
	case "/web/modules/htmlvideoplayer/plugin.js":
		return embyWebPatchPlugin
	case "/web/index.html", "/web", "/web/":
		return embyWebPatchIndex
	default:
		return embyWebPatchNone
	}
}

func (kind embyWebPatchKind) String() string {
	switch kind {
	case embyWebPatchBasePlayer:
		return "basehtmlplayer"
	case embyWebPatchPlugin:
		return "plugin"
	case embyWebPatchIndex:
		return "index"
	default:
		return "unknown"
	}
}

func buildEmbyWebCompatibilityScript(target embyGatewayTarget) string {
	script := strings.ReplaceAll(embyWebCompatibilityScriptTemplate, "__EXTERNAL_PLAYER__", strconv.FormatBool(target.ExternalPlayerEnabled))
	return strings.ReplaceAll(script, "__FANART__", strconv.FormatBool(target.FanartEnabled))
}

func serveEmbyWebCompatibilityAsset(w http.ResponseWriter, r *http.Request, gatewayPath string, target embyGatewayTarget) bool {
	path := strings.ToLower(normalizeGatewayPath(gatewayPath))
	if strings.HasPrefix(path, "/emby/") {
		path = strings.TrimPrefix(path, "/emby")
	}
	if path != embyWebCompatibilityAssetPath {
		return false
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return true
	}
	script := buildEmbyWebCompatibilityScript(target)
	w.Header().Set("Content-Length", strconv.Itoa(len(script)))
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, script)
	}
	return true
}

func (s *EmbyGatewayService) rewritePlaybackInfo(gateway embyGatewayTarget, itemID string, body []byte) ([]byte, int, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, errors.New("emby playback response is invalid")
	}
	sources, ok := payload["MediaSources"].([]any)
	if !ok {
		return body, 0, nil
	}
	changed := 0
	for _, raw := range sources {
		source, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		pathValue, _ := source["Path"].(string)
		verified, err := s.signedProxy.VerifyArtifactURL(pathValue)
		if err != nil {
			continue
		}
		sourceID, _ := source["Id"].(string)
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			continue
		}
		ticket, err := s.issueTicket(playbackGrant{GatewayID: gateway.PublicID, PolicyRevision: gateway.PolicyRevision, ItemID: itemID, MediaSource: sourceID, Artifact: verified.Opaque})
		if err != nil {
			return nil, 0, errors.New("issue emby playback ticket")
		}
		kind := "videos"
		if sourceType, _ := source["Type"].(string); strings.EqualFold(sourceType, "audio") {
			kind = "audio"
		}
		query := url.Values{"Static": {"true"}, "MediaSourceId": {sourceID}, playbackTicketParam: {ticket}}
		// Emby clients resolve DirectStreamUrl against their API base. For an
		// Emby Web session whose configured server is /emby/<gateway>, that API
		// base is /emby/<gateway>/emby. Including the outer OhMyCine gateway
		// mount here would therefore produce
		// /emby/<gateway>/emby/emby/<gateway>/videos/... and invalidate the
		// ticket route binding. Keep both fields Emby API-relative; the client
		// supplies the gateway and Emby application prefixes exactly once.
		streamURL := "/" + kind + "/" + url.PathEscape(itemID) + "/stream?" + query.Encode()
		source["Path"] = streamURL
		source["DirectStreamUrl"] = streamURL
		source["SupportsDirectPlay"] = true
		source["SupportsDirectStream"] = true
		source["SupportsTranscoding"] = false
		delete(source, "TranscodingUrl")
		delete(source, "TranscodingContainer")
		delete(source, "TranscodingSubProtocol")
		changed++
	}
	if changed == 0 {
		return body, 0, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, errors.New("encode emby playback response")
	}
	return encoded, changed, nil
}

func (s *EmbyGatewayService) issueTicket(grant playbackGrant) (string, error) {
	nonceBytes := make([]byte, 18)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	grant.ExpiresAt = s.now().Add(embyTicketTTL)
	expiry := grant.ExpiresAt.Unix()
	signature := base64.RawURLEncoding.EncodeToString(hmacSHA256(s.secret, playbackTicketCanonical(nonce, expiry, grant)))
	s.mu.Lock()
	s.pruneGrantsLocked(s.now())
	if len(s.grants) >= embyTicketMaximum {
		for candidate := range s.grants {
			delete(s.grants, candidate)
			break
		}
	}
	s.grants[nonce] = grant
	s.mu.Unlock()
	return "v1." + strconv.FormatInt(expiry, 10) + "." + nonce + "." + signature, nil
}

func (s *EmbyGatewayService) verifyTicket(ticket, gatewayID, itemID, mediaSource string) (playbackGrant, error) {
	parts := strings.Split(ticket, ".")
	if len(parts) != 4 || parts[0] != "v1" || !validOpaqueID(parts[2]) {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	now := s.now()
	s.mu.Lock()
	s.pruneGrantsLocked(now)
	grant, ok := s.grants[parts[2]]
	s.mu.Unlock()
	if !ok || expiry != grant.ExpiresAt.Unix() || !now.Before(grant.ExpiresAt) || grant.GatewayID != gatewayID || grant.ItemID != itemID || grant.MediaSource != mediaSource {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(want) != sha256.Size || base64.RawURLEncoding.EncodeToString(want) != parts[3] {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	got := hmacSHA256(s.secret, playbackTicketCanonical(parts[2], expiry, grant))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	// Re-read the persisted policy after authenticating the opaque ticket. The
	// request-level gateway lookup is intentionally insufficient: an endpoint,
	// credential, or gateway policy update may commit while the request is in
	// flight, and a ticket from the previous revision must fail closed.
	current, err := s.gatewayByPublicID(gatewayID)
	if err != nil || !current.GatewayEnabled || !current.ConnectionEnabled || current.HealthStatus != "online" || current.PolicyRevision != grant.PolicyRevision {
		return playbackGrant{}, appError(CodeEmbyPlaybackTicketInvalid, "播放票据无效", nil)
	}
	return grant, nil
}

func playbackTicketCanonical(nonce string, expiry int64, grant playbackGrant) []byte {
	return []byte("v1\nmedia-read\n" + nonce + "\n" + grant.GatewayID + "\n" + strconv.FormatUint(grant.PolicyRevision, 10) + "\n" + grant.ItemID + "\n" + grant.MediaSource + "\n" + grant.Artifact + "\n" + strconv.FormatInt(expiry, 10))
}

func (s *EmbyGatewayService) pruneGrantsLocked(now time.Time) {
	for nonce, grant := range s.grants {
		if !now.Before(grant.ExpiresAt) {
			delete(s.grants, nonce)
		}
	}
}

func (s *EmbyGatewayService) boundPlaybackRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > embyPlaybackBodyLimit {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return false
	}
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, embyPlaybackBodyLimit+1))
	_ = r.Body.Close()
	if err != nil || len(body) > embyPlaybackBodyLimit {
		http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return true
}

func (s *EmbyGatewayService) gatewayByConnection(connectionID uint) (embyGatewayTarget, error) {
	var target embyGatewayTarget
	err := s.db.Table("emby_proxy_gateways").Select(`emby_proxy_gateways.id AS gateway_id, emby_proxy_gateways.connection_id,
		emby_proxy_gateways.public_id, emby_proxy_gateways.enabled AS gateway_enabled, emby_proxy_gateways.external_player_enabled, emby_proxy_gateways.fanart_enabled, emby_proxy_gateways.policy_revision,
		connections.endpoint, connections.provider, connections.enabled AS connection_enabled, connections.last_health_status AS health_status`).
		Joins("JOIN connections ON connections.id = emby_proxy_gateways.connection_id").
		Where("connections.id = ? AND connections.provider = ?", connectionID, models.ConnectionProviderEmby).Take(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return embyGatewayTarget{}, appError(CodeNotFound, "Emby 网关不存在", nil)
	}
	return target, err
}

func (s *EmbyGatewayService) gatewayByPublicID(publicID string) (embyGatewayTarget, error) {
	if !validExistingGatewayID(publicID) {
		return embyGatewayTarget{}, appError(CodeNotFound, "Emby 网关不存在", nil)
	}
	var target embyGatewayTarget
	err := s.db.Table("emby_proxy_gateways").Select(`emby_proxy_gateways.id AS gateway_id, emby_proxy_gateways.connection_id,
		emby_proxy_gateways.public_id, emby_proxy_gateways.enabled AS gateway_enabled, emby_proxy_gateways.external_player_enabled, emby_proxy_gateways.fanart_enabled, emby_proxy_gateways.policy_revision,
		connections.endpoint, connections.provider, connections.enabled AS connection_enabled, connections.last_health_status AS health_status`).
		Joins("JOIN connections ON connections.id = emby_proxy_gateways.connection_id").
		Where("emby_proxy_gateways.public_id = ? AND connections.provider = ?", publicID, models.ConnectionProviderEmby).Take(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return embyGatewayTarget{}, appError(CodeNotFound, "Emby 网关不存在", nil)
	}
	return target, err
}

func (s *EmbyGatewayService) summary(target embyGatewayTarget) EmbyGatewaySummary {
	return EmbyGatewaySummary{ConnectionID: target.ConnectionID, PublicID: target.PublicID, Alias: target.PublicID, Enabled: target.GatewayEnabled, ExternalPlayerEnabled: target.ExternalPlayerEnabled, FanartEnabled: target.FanartEnabled, BaseURL: s.publicOrigin + "/emby/" + url.PathEscape(target.PublicID), Revision: target.PolicyRevision}
}

func normalizeEmbyGatewayAlias(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < embyAliasMinLength || len(value) > embyAliasMaxLength || !embyAliasPattern.MatchString(value) {
		return "", appError(CodeEmbyGatewayAliasInvalid, "网关别名需为 3-32 位小写字母、数字或单个连字符", nil)
	}
	if _, reserved := embyAliasReserved[value]; reserved {
		return "", appError(CodeEmbyGatewayAliasReserved, "该网关别名为系统保留字", nil)
	}
	return value, nil
}

func validExistingGatewayID(value string) bool {
	if _, err := normalizeEmbyGatewayAlias(value); err == nil {
		return true
	}
	// v27 generated opaque IDs remain routable until the administrator saves
	// a short alias. They are never silently rewritten during startup.
	return validOpaqueID(value)
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func (s *EmbyGatewayService) rewriteLocation(gateway embyGatewayTarget, requestURL *url.URL, location string) (string, bool) {
	reference, err := url.Parse(location)
	if err != nil || reference.User != nil || requestURL == nil {
		return "", false
	}
	upstream, err := emby.ParseEndpoint(gateway.Endpoint)
	if err != nil {
		return "", false
	}
	resolved := requestURL.ResolveReference(reference)
	if resolved.User != nil {
		return "", false
	}
	if !sameOrigin(resolved, upstream) {
		if !reference.IsAbs() {
			// Never let a scheme-relative redirect resolve against OhMyCine's
			// own origin. Preserve its exact upstream destination instead.
			return resolved.String(), true
		}
		return location, false
	}
	pathValue := resolved.Path
	if upstream.Path != "" {
		if pathValue == upstream.Path {
			pathValue = "/"
		} else if strings.HasPrefix(pathValue, upstream.Path+"/") {
			pathValue = strings.TrimPrefix(pathValue, upstream.Path)
		} else {
			if !reference.IsAbs() {
				// A relative Location outside the configured endpoint prefix must
				// not escape into OhMyCine routes when the client resolves it.
				return resolved.String(), true
			}
			return location, false
		}
	}
	resolved.Scheme, resolved.Host, resolved.User = "", "", nil
	resolved.Path = "/emby/" + url.PathEscape(gateway.PublicID) + normalizeGatewayPath(pathValue)
	resolved.RawPath = ""
	return resolved.String(), true
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Hostname(), right.Hostname()) && effectiveURLPort(left) == effectiveURLPort(right)
}

func effectiveURLPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

func normalizeGatewayPath(value string) string {
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}

func safeGatewayPath(value, rawPath string) (string, bool) {
	value = normalizeGatewayPath(value)
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return "", false
	}
	if containsEncodedGatewayEscape(value) || containsEncodedGatewayEscape(rawPath) {
		return "", false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	return value, true
}

func containsEncodedGatewayEscape(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "%25") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00")
}

func joinGatewayPath(base, requestPath string) string {
	base = strings.TrimRight(base, "/")
	requestPath = normalizeGatewayPath(requestPath)
	if base == "" {
		return requestPath
	}
	// Emby Web may include the configured application base path in requests
	// routed through the outer OhMyCine gateway mount. Preserve that prefix
	// exactly once, while avoiding prefix collisions such as /emby-other.
	if requestPath == base || strings.HasPrefix(requestPath, base+"/") {
		return requestPath
	}
	return base + requestPath
}

func playbackInfoItem(value string) (string, bool) {
	parts := routeParts(value)
	if len(parts) == 3 && strings.EqualFold(parts[0], "items") && strings.EqualFold(parts[2], "playbackinfo") && parts[1] != "" {
		return parts[1], true
	}
	return "", false
}

func mediaRouteBinding(value string) (string, bool) {
	parts := routeParts(value)
	if len(parts) == 3 && (strings.EqualFold(parts[0], "videos") || strings.EqualFold(parts[0], "audio")) && isEmbyStreamName(parts[2]) {
		return parts[1], parts[1] != ""
	}
	if len(parts) == 3 && strings.EqualFold(parts[0], "items") && (strings.EqualFold(parts[2], "download") || strings.EqualFold(parts[2], "file")) {
		return parts[1], parts[1] != ""
	}
	if len(parts) == 4 && strings.EqualFold(parts[0], "sync") && strings.EqualFold(parts[1], "jobitems") && strings.EqualFold(parts[3], "file") {
		return parts[2], parts[2] != ""
	}
	return "", false
}

func isEmbyStreamName(value string) bool {
	value = strings.ToLower(value)
	return value == "stream" || strings.HasPrefix(value, "stream.")
}

func routeParts(value string) []string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) > 0 && strings.EqualFold(parts[0], "emby") {
		parts = parts[1:]
	}
	return parts
}

func singleQueryFold(query url.Values, name string) (string, bool, bool) {
	var result string
	found := false
	for key, values := range query {
		if !strings.EqualFold(key, name) {
			continue
		}
		if found || len(values) != 1 {
			return "", true, false
		}
		result, found = values[0], true
	}
	return result, found, true
}

func rawQueryHasFold(rawQuery, name string) bool {
	for _, field := range strings.FieldsFunc(rawQuery, func(character rune) bool { return character == '&' || character == ';' }) {
		key := field
		if separator := strings.IndexByte(key, '='); separator >= 0 {
			key = key[:separator]
		}
		for attempt := 0; attempt <= len(key); attempt++ {
			if strings.EqualFold(key, name) {
				return true
			}
			decoded, err := url.QueryUnescape(key)
			if err != nil {
				break
			}
			if decoded == key {
				break
			}
			key = decoded
		}
	}
	return false
}

func isWebSocketUpgrade(header http.Header) bool {
	if !strings.EqualFold(strings.TrimSpace(header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}
