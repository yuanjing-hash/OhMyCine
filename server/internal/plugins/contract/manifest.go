package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	APIVersion       = "1"
	MaxManifestBytes = 256 * 1024
	MaxPermissions   = 64
)

var (
	pluginIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)+$`)
	domainPattern   = regexp.MustCompile(`^(?:\*\.)?[a-z0-9.-]+$`)
	scopePattern    = regexp.MustCompile(`^[a-z0-9.-]+$`)
)

const maxPluginVersionLength = 128

type semanticVersion struct {
	core       [3]string
	prerelease []string
}

type Capability string

const (
	CapabilitySiteNavigation   Capability = "site.navigation"
	CapabilitySiteFeed         Capability = "site.feed"
	CapabilitySiteSearch       Capability = "site.search"
	CapabilitySiteDetail       Capability = "site.detail"
	CapabilitySiteUserLibrary  Capability = "site.user_library"
	CapabilitySiteInteraction  Capability = "site.interaction"
	CapabilityMediaPlayback    Capability = "media.playback"
	CapabilityQualitySwitch    Capability = "media.quality_switch"
	CapabilityMediaSubtitle    Capability = "media.subtitle"
	CapabilityMediaDanmaku     Capability = "media.danmaku"
	CapabilityMediaDownload    Capability = "media.download_plan"
	CapabilityMediaMetadata    Capability = "media.metadata"
	CapabilityHomeContribution Capability = "home.contribution"
	CapabilityFeedRefresh      Capability = "feed.refresh"
	CapabilitySiteHistory      Capability = "site.history"
	CapabilityPlaybackProgress Capability = "playback.progress_sync"
	CapabilityLibraryArtwork   Capability = "library.artwork_candidates"
)

var knownCapabilities = map[Capability]struct{}{
	CapabilitySiteNavigation: {}, CapabilitySiteFeed: {}, CapabilitySiteSearch: {}, CapabilitySiteDetail: {},
	CapabilitySiteUserLibrary: {}, CapabilitySiteInteraction: {}, CapabilityMediaPlayback: {},
	CapabilityQualitySwitch: {}, CapabilityMediaSubtitle: {}, CapabilityMediaDanmaku: {},
	CapabilityMediaDownload: {}, CapabilityHomeContribution: {}, CapabilityFeedRefresh: {},
	CapabilityMediaMetadata: {},
	CapabilitySiteHistory:   {}, CapabilityPlaybackProgress: {}, CapabilityLibraryArtwork: {},
}

type PermissionKind string

const (
	PermissionNetworkHTTP    PermissionKind = "network.http"
	PermissionCredentialUse  PermissionKind = "credential.use"
	PermissionPrivateStorage PermissionKind = "storage.private"
	PermissionEventSubscribe PermissionKind = "event.subscribe"
	PermissionDownloadPlan   PermissionKind = "download.plan"
)

type Permission struct {
	Kind     PermissionKind `json:"kind"`
	Domains  []string       `json:"domains,omitempty"`
	Scopes   []string       `json:"scopes,omitempty"`
	Topics   []string       `json:"topics,omitempty"`
	MaxBytes *int64         `json:"maxBytes,omitempty"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	Value     string `json:"value"`
}

type UpdateInfo struct {
	RegistryURL string `json:"registryUrl"`
	Channel     string `json:"channel,omitempty"`
}

type Manifest struct {
	SchemaVersion    int             `json:"schemaVersion"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Version          string          `json:"version"`
	APIVersion       string          `json:"apiVersion"`
	MinServerVersion string          `json:"minServerVersion"`
	MaxServerVersion string          `json:"maxServerVersion,omitempty"`
	Runtime          string          `json:"runtime"`
	Entry            string          `json:"entry"`
	LibraryArtwork   string          `json:"libraryArtwork,omitempty"`
	Capabilities     []Capability    `json:"capabilities"`
	NavigationMode   string          `json:"navigationMode,omitempty"`
	Permissions      []Permission    `json:"permissions"`
	ConfigSchema     json.RawMessage `json:"configSchema"`
	SettingsPage     *SettingsPage   `json:"settingsPage,omitempty"`
	Author           string          `json:"author"`
	License          string          `json:"license"`
	Homepage         string          `json:"homepage,omitempty"`
	Source           string          `json:"source"`
	PackageSHA256    string          `json:"packageSha256"`
	Signature        *Signature      `json:"signature,omitempty"`
	Update           *UpdateInfo     `json:"update,omitempty"`
	Changelog        string          `json:"changelog,omitempty"`
}

func ParseManifest(data []byte) (Manifest, error) {
	if len(data) == 0 {
		return Manifest{}, errors.New("plugin manifest is empty")
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("plugin manifest exceeds %d bytes", MaxManifestBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return Manifest{}, errors.New("plugin manifest contains trailing JSON values")
	} else if !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("plugin manifest contains invalid trailing data")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported plugin manifest schema version %d", manifest.SchemaVersion)
	}
	if manifest.APIVersion != APIVersion {
		return fmt.Errorf("unsupported plugin API version %q", manifest.APIVersion)
	}
	if len(manifest.ID) == 0 || len(manifest.ID) > 128 || !pluginIDPattern.MatchString(manifest.ID) {
		return errors.New("plugin id is invalid")
	}
	if strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 80 {
		return errors.New("plugin name is invalid")
	}
	if strings.TrimSpace(manifest.Description) == "" || len(manifest.Description) > 500 {
		return errors.New("plugin description is invalid")
	}
	if !validPluginVersion(manifest.Version) || !validPluginVersion(manifest.MinServerVersion) {
		return errors.New("plugin version range is invalid")
	}
	if manifest.MaxServerVersion != "" {
		if !validPluginVersion(manifest.MaxServerVersion) {
			return errors.New("plugin maximum server version is invalid")
		}
		comparison, _ := CompareVersions(manifest.MinServerVersion, manifest.MaxServerVersion)
		if comparison > 0 {
			return errors.New("plugin server version range is reversed")
		}
	}
	if manifest.Runtime != "wasm" {
		return errors.New("only the wasm plugin runtime is supported")
	}
	if !safePackageEntry(manifest.Entry) {
		return errors.New("plugin entry must be a package-relative wasm path")
	}
	if manifest.LibraryArtwork != "" && !safeLibraryArtwork(manifest.LibraryArtwork) {
		return errors.New("plugin libraryArtwork must be a package-relative PNG, JPEG, or WebP path")
	}
	if err := validateCapabilities(manifest.Capabilities); err != nil {
		return err
	}
	if manifest.NavigationMode != "" && manifest.NavigationMode != "flat" && manifest.NavigationMode != "hierarchical" {
		return errors.New("plugin navigationMode is invalid")
	}
	if manifest.NavigationMode == "hierarchical" && !manifestHasCapabilityValue(manifest.Capabilities, CapabilitySiteNavigation) {
		return errors.New("hierarchical navigation requires site.navigation capability")
	}
	if len(manifest.Permissions) > MaxPermissions {
		return errors.New("plugin permission list is too large")
	}
	seenPermissions := make(map[string]struct{}, len(manifest.Permissions))
	for index, permission := range manifest.Permissions {
		if err := permission.Validate(); err != nil {
			return fmt.Errorf("permission %d: %w", index, err)
		}
		canonical, err := json.Marshal(permission)
		if err != nil {
			return fmt.Errorf("permission %d: encode canonical permission: %w", index, err)
		}
		key := string(canonical)
		if _, exists := seenPermissions[key]; exists {
			return fmt.Errorf("permission %d: duplicate permission", index)
		}
		seenPermissions[key] = struct{}{}
	}
	if !isJSONObject(manifest.ConfigSchema) {
		return errors.New("plugin configSchema must be a JSON object")
	}
	if manifest.SettingsPage != nil {
		if err := manifest.SettingsPage.Validate(manifest.ConfigSchema); err != nil {
			return fmt.Errorf("plugin settingsPage is invalid: %w", err)
		}
	}
	if strings.TrimSpace(manifest.Author) == "" || strings.TrimSpace(manifest.License) == "" {
		return errors.New("plugin author and license are required")
	}
	if !validHTTPSURL(manifest.Source) || (manifest.Homepage != "" && !validHTTPSURL(manifest.Homepage)) {
		return errors.New("plugin source and homepage must use HTTPS")
	}
	if _, err := DecodeSHA256(manifest.PackageSHA256); err != nil {
		return err
	}
	if manifest.Signature != nil {
		if manifest.Signature.Algorithm != "ed25519" || strings.TrimSpace(manifest.Signature.KeyID) == "" || len(manifest.Signature.Value) < 32 {
			return errors.New("plugin signature is invalid")
		}
	}
	if manifest.Update != nil {
		if !validHTTPSURL(manifest.Update.RegistryURL) || (manifest.Update.Channel != "" && manifest.Update.Channel != "stable" && manifest.Update.Channel != "beta") {
			return errors.New("plugin update information is invalid")
		}
	}
	return nil
}

func manifestHasCapabilityValue(capabilities []Capability, wanted Capability) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

// CompareVersions compares the strict SemVer subset accepted by plugin
// contracts. Build metadata is intentionally unsupported so one version has a
// single canonical representation in registries, caches, and UI selection.
func CompareVersions(left, right string) (int, error) {
	leftVersion, err := parsePluginVersion(left)
	if err != nil {
		return 0, err
	}
	rightVersion, err := parsePluginVersion(right)
	if err != nil {
		return 0, err
	}
	for index := range leftVersion.core {
		if comparison := compareNumericIdentifier(leftVersion.core[index], rightVersion.core[index]); comparison != 0 {
			return comparison, nil
		}
	}
	if len(leftVersion.prerelease) == 0 && len(rightVersion.prerelease) == 0 {
		return 0, nil
	}
	if len(leftVersion.prerelease) == 0 {
		return 1, nil
	}
	if len(rightVersion.prerelease) == 0 {
		return -1, nil
	}
	maximum := len(leftVersion.prerelease)
	if len(rightVersion.prerelease) < maximum {
		maximum = len(rightVersion.prerelease)
	}
	for index := 0; index < maximum; index++ {
		leftPart, rightPart := leftVersion.prerelease[index], rightVersion.prerelease[index]
		leftNumeric, rightNumeric := numericIdentifier(leftPart), numericIdentifier(rightPart)
		switch {
		case leftNumeric && rightNumeric:
			if comparison := compareNumericIdentifier(leftPart, rightPart); comparison != 0 {
				return comparison, nil
			}
		case leftNumeric:
			return -1, nil
		case rightNumeric:
			return 1, nil
		case leftPart < rightPart:
			return -1, nil
		case leftPart > rightPart:
			return 1, nil
		}
	}
	switch {
	case len(leftVersion.prerelease) < len(rightVersion.prerelease):
		return -1, nil
	case len(leftVersion.prerelease) > len(rightVersion.prerelease):
		return 1, nil
	default:
		return 0, nil
	}
}

func validPluginVersion(value string) bool {
	_, err := parsePluginVersion(value)
	return err == nil
}

func parsePluginVersion(value string) (semanticVersion, error) {
	if value == "" || len(value) > maxPluginVersionLength || strings.Contains(value, "+") {
		return semanticVersion{}, errors.New("plugin version is invalid")
	}
	core, prerelease, hasPrerelease := strings.Cut(value, "-")
	coreParts := strings.Split(core, ".")
	if len(coreParts) != 3 {
		return semanticVersion{}, errors.New("plugin version core is invalid")
	}
	var parsed semanticVersion
	for index, part := range coreParts {
		if !validNumericIdentifier(part) {
			return semanticVersion{}, errors.New("plugin version core is invalid")
		}
		parsed.core[index] = part
	}
	if !hasPrerelease {
		return parsed, nil
	}
	parts := strings.Split(prerelease, ".")
	if len(parts) == 0 {
		return semanticVersion{}, errors.New("plugin prerelease is invalid")
	}
	for _, part := range parts {
		if part == "" {
			return semanticVersion{}, errors.New("plugin prerelease is invalid")
		}
		for _, character := range part {
			if (character < '0' || character > '9') && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && character != '-' {
				return semanticVersion{}, errors.New("plugin prerelease is invalid")
			}
		}
		if numericIdentifier(part) && len(part) > 1 && part[0] == '0' {
			return semanticVersion{}, errors.New("plugin prerelease numeric identifier has a leading zero")
		}
	}
	parsed.prerelease = parts
	return parsed, nil
}

func validNumericIdentifier(value string) bool {
	return numericIdentifier(value) && (len(value) == 1 || value[0] != '0')
}

func numericIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func compareNumericIdentifier(left, right string) int {
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return strings.Compare(left, right)
}

func (permission Permission) Validate() error {
	switch permission.Kind {
	case PermissionNetworkHTTP:
		if len(permission.Domains) == 0 || permission.MaxBytes != nil || len(permission.Scopes) > 0 || len(permission.Topics) > 0 {
			return errors.New("network.http requires only a non-empty domains list")
		}
		return validateUniqueStrings(permission.Domains, domainPattern, "network domain")
	case PermissionCredentialUse:
		if len(permission.Scopes) == 0 || permission.MaxBytes != nil || len(permission.Domains) > 0 || len(permission.Topics) > 0 {
			return errors.New("credential.use requires only a non-empty scopes list")
		}
		return validateUniqueStrings(permission.Scopes, scopePattern, "credential scope")
	case PermissionPrivateStorage:
		if permission.MaxBytes == nil || *permission.MaxBytes < 0 || *permission.MaxBytes > 64*1024*1024 || len(permission.Domains) > 0 || len(permission.Scopes) > 0 || len(permission.Topics) > 0 {
			return errors.New("storage.private requires maxBytes between 0 and 67108864")
		}
	case PermissionEventSubscribe:
		if len(permission.Topics) == 0 || permission.MaxBytes != nil || len(permission.Domains) > 0 || len(permission.Scopes) > 0 {
			return errors.New("event.subscribe requires only a non-empty topics list")
		}
		return validateUniqueStrings(permission.Topics, scopePattern, "event topic")
	case PermissionDownloadPlan:
		if permission.MaxBytes != nil || len(permission.Domains) > 0 || len(permission.Scopes) > 0 || len(permission.Topics) > 0 {
			return errors.New("download.plan accepts no additional fields")
		}
	default:
		return fmt.Errorf("unknown permission kind %q", permission.Kind)
	}
	return nil
}

func DecodeSHA256(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return digest, errors.New("packageSha256 must be a lowercase SHA-256 digest")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func safePackageEntry(value string) bool {
	if value == "" || len(value) > 240 || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && !path.IsAbs(cleaned) && cleaned != "." && !strings.HasPrefix(cleaned, "../") && strings.HasSuffix(strings.ToLower(cleaned), ".wasm")
}

func safeLibraryArtwork(value string) bool {
	if value == "" || len(value) > 240 || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') {
		return false
	}
	cleaned := path.Clean(value)
	if cleaned != value || path.IsAbs(cleaned) || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	extension := strings.ToLower(path.Ext(cleaned))
	return extension == ".png" || extension == ".jpg" || extension == ".jpeg" || extension == ".webp"
}

func validateCapabilities(capabilities []Capability) error {
	seen := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if _, ok := knownCapabilities[capability]; !ok {
			return fmt.Errorf("unknown plugin capability %q", capability)
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("duplicate plugin capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateUniqueStrings(values []string, pattern *regexp.Regexp, label string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !pattern.MatchString(value) {
			return fmt.Errorf("invalid %s", label)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("duplicate %s", label)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func isJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid(trimmed)
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
