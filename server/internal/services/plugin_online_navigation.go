package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

const (
	maxPluginNavigationDepth = 8
	maxPluginNavigationNodes = 100
	pluginNavigationTokenTTL = 24 * time.Hour
)

type pluginNavigationClaim struct {
	LibraryID string   `json:"libraryId"`
	Kind      string   `json:"kind"`
	NodeKey   string   `json:"nodeKey"`
	Depth     int      `json:"depth"`
	Ancestors []string `json:"ancestors"`
	ExpiresAt int64    `json:"expiresAt"`
}

type pluginNavigationResponse struct {
	Version int                    `json:"version"`
	Mode    string                 `json:"mode"`
	Nodes   []pluginNavigationNode `json:"nodes"`
}

type pluginNavigationNode struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	NodeKey     string `json:"nodeKey,omitempty"`
	NodeToken   string `json:"nodeToken,omitempty"`
	RouteKey    string `json:"routeKey,omitempty"`
	HasChildren bool   `json:"hasChildren,omitempty"`
	Refreshable bool   `json:"refreshable,omitempty"`
}

func (s *PluginRepositoryService) OnlineNavigationChildren(ctx context.Context, actor Actor, libraryID, token string) (json.RawMessage, error) {
	claim, err := s.verifyPluginNavigationToken(token)
	if err != nil || claim.LibraryID != libraryID || claim.Depth < 1 || claim.Depth >= maxPluginNavigationDepth {
		return nil, appError(CodeInvalidRequest, "在线媒体导航节点无效", err)
	}
	_, _, manifest, err := s.onlineLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if manifest.NavigationMode != "hierarchical" {
		return nil, appError(CodePermissionDenied, "在线媒体库不支持层级导航", nil)
	}
	raw, err := s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteNavigation, map[string]any{
		"connectionId": libraryID, "parentNodeKey": claim.NodeKey, "depth": claim.Depth,
	})
	if err != nil {
		return nil, err
	}
	normalized, err := s.normalizeHierarchicalNavigation(libraryID, raw, claim.Depth, claim.Ancestors)
	if err != nil {
		s.logInvalidOnlineNavigation(libraryID, manifest.ID, err)
		return nil, err
	}
	return normalized, nil
}

func (s *PluginRepositoryService) logInvalidOnlineNavigation(libraryID, pluginID string, err error) {
	serverlog.OperationPluginRuntime.Event(s.log.Warn()).
		Str("plugin_id", safeLabel(pluginID, 128)).
		Str("library_id", safeLabel(libraryID, 128)).
		Str("capability", string(contract.CapabilitySiteNavigation)).
		Str("error_code", safeLabel(ErrorCode(err), 96)).
		Msg(serverlog.OperationPluginRuntime.Message("插件导航响应校验失败"))
}

func (s *PluginRepositoryService) normalizeOnlineNavigation(libraryID string, manifest contract.Manifest, raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if manifest.NavigationMode != "hierarchical" {
		if len(raw) == 0 || raw[0] != '[' {
			return nil, appError(CodePluginResponseInvalid, "在线媒体导航响应无效", nil)
		}
		return append(json.RawMessage(nil), raw...), nil
	}
	return s.normalizeHierarchicalNavigation(libraryID, raw, 0, nil)
}

func (s *PluginRepositoryService) normalizeHierarchicalNavigation(libraryID string, raw json.RawMessage, depth int, ancestors []string) (json.RawMessage, error) {
	var input struct {
		Version int                    `json:"version"`
		Mode    string                 `json:"mode"`
		Nodes   []pluginNavigationNode `json:"nodes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || input.Version != 2 || input.Mode != "hierarchical" || len(input.Nodes) > maxPluginNavigationNodes {
		return nil, appError(CodePluginResponseInvalid, "在线媒体导航响应无效", err)
	}
	seen := make(map[string]struct{}, len(input.Nodes))
	result := pluginNavigationResponse{Version: 2, Mode: "hierarchical", Nodes: make([]pluginNavigationNode, 0, len(input.Nodes))}
	for _, node := range input.Nodes {
		if !safeOnlineText(node.ID, 128) || !safeOnlineText(node.Title, 256) {
			return nil, appError(CodePluginResponseInvalid, "在线媒体导航节点无效", nil)
		}
		if _, exists := seen[node.ID]; exists {
			return nil, appError(CodePluginResponseInvalid, "在线媒体导航节点重复", nil)
		}
		seen[node.ID] = struct{}{}
		normalized := pluginNavigationNode{ID: node.ID, Title: node.Title, Kind: node.Kind, Refreshable: node.Refreshable}
		switch node.Kind {
		case "branch":
			if !safeOnlineText(node.NodeKey, 256) || depth+1 > maxPluginNavigationDepth || containsNavigationAncestor(ancestors, node.NodeKey) {
				return nil, appError(CodePluginResponseInvalid, "在线媒体导航分支无效", nil)
			}
			nextAncestors := append(append([]string(nil), ancestors...), node.NodeKey)
			token, err := s.signPluginNavigationToken(pluginNavigationClaim{LibraryID: libraryID, Kind: "branch", NodeKey: node.NodeKey, Depth: depth + 1, Ancestors: nextAncestors, ExpiresAt: time.Now().UTC().Add(pluginNavigationTokenTTL).Unix()})
			if err != nil {
				return nil, appError(CodePluginResponseInvalid, "在线媒体导航节点签发失败", err)
			}
			normalized.NodeToken, normalized.HasChildren = token, true
		case "feed", "search", "user-library":
			if !safeOnlineText(node.RouteKey, 256) {
				return nil, appError(CodePluginResponseInvalid, "在线媒体导航栏目无效", nil)
			}
			normalized.RouteKey = node.RouteKey
		default:
			return nil, appError(CodePluginResponseInvalid, "在线媒体导航类型无效", nil)
		}
		result.Nodes = append(result.Nodes, normalized)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线媒体导航响应无效", err)
	}
	return json.RawMessage(encoded), nil
}

func containsNavigationAncestor(ancestors []string, key string) bool {
	for _, ancestor := range ancestors {
		if hmac.Equal([]byte(ancestor), []byte(key)) {
			return true
		}
	}
	return false
}

func (s *PluginRepositoryService) signPluginNavigationToken(claim pluginNavigationClaim) (string, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.navigationKey[:])
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *PluginRepositoryService) verifyPluginNavigationToken(token string) (pluginNavigationClaim, error) {
	if len(token) == 0 || len(token) > 4096 || strings.Count(token, ".") != 1 {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	parts := strings.SplitN(token, ".", 2)
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(body) > 2048 {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	mac := hmac.New(sha256.New, s.navigationKey[:])
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	var claim pluginNavigationClaim
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claim); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || !safeOnlineText(claim.LibraryID, 128) || claim.Kind != "branch" || !safeOnlineText(claim.NodeKey, 256) || claim.Depth < 1 || claim.Depth > maxPluginNavigationDepth || len(claim.Ancestors) != claim.Depth || claim.ExpiresAt < time.Now().UTC().Unix() {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	for index, ancestor := range claim.Ancestors {
		if !safeOnlineText(ancestor, 256) || index > 0 && containsNavigationAncestor(claim.Ancestors[:index], ancestor) {
			return pluginNavigationClaim{}, errors.New("navigation token is invalid")
		}
	}
	if !hmac.Equal([]byte(claim.Ancestors[len(claim.Ancestors)-1]), []byte(claim.NodeKey)) {
		return pluginNavigationClaim{}, errors.New("navigation token is invalid")
	}
	return claim, nil
}
