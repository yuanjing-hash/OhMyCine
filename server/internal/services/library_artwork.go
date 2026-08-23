package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	"gorm.io/gorm"
)

const (
	libraryArtworkTemplateVersion = "library-artwork-v1"
	libraryArtworkWidth           = 1280
	libraryArtworkHeight          = 720
	libraryArtworkCandidateLimit  = 9
	libraryArtworkRenderLimit     = 4
	libraryArtworkMaxSourceBytes  = 8 << 20
	libraryArtworkMaxDimension    = 8192
	libraryArtworkMaxPixels       = 36_000_000
	libraryArtworkCacheLimit      = 256
	libraryArtworkTicketTTL       = 15 * time.Minute
)

type PluginArtworkAssetGateway interface {
	OpenAssetForPluginConnection(context.Context, string, string, string, string, string) (*hostapi.AssetStream, error)
}

type LibraryArtworkAsset struct {
	Digest string
	Bytes  []byte
}

type artworkCandidate struct {
	key  string
	load func(context.Context) ([]byte, error)
}

// LibraryArtworkService owns generated presentation assets for libraries
// indexed by Server. It never exposes candidate URLs, provider identities, or
// credentials: callers receive only an opaque content digest and JPEG bytes.
type LibraryArtworkService struct {
	db       *gorm.DB
	metadata *MetadataSettingsService
	plugins  *PluginRepositoryService
	assets   PluginArtworkAssetGateway
	log      zerolog.Logger

	mu    sync.RWMutex
	cache map[string][]byte
	// generation maps the deterministic candidate/template key to the digest
	// of the actual encoded JPEG. Public immutable URLs always use the latter.
	generation map[string]string
	order      []string
	signingKey [32]byte
	now        func() time.Time
}

func NewLibraryArtworkService(db *gorm.DB, metadata *MetadataSettingsService, plugins *PluginRepositoryService, assets PluginArtworkAssetGateway, log zerolog.Logger) *LibraryArtworkService {
	var signingKey [32]byte
	if _, err := rand.Read(signingKey[:]); err != nil {
		panic("library artwork signing key unavailable")
	}
	return &LibraryArtworkService{
		db: db, metadata: metadata, plugins: plugins, assets: assets, log: log,
		cache: make(map[string][]byte), generation: make(map[string]string), signingKey: signingKey, now: time.Now,
	}
}

func (s *LibraryArtworkService) DecorateMediaLibraries(ctx context.Context, libraries []PlayerMediaLibrary) []PlayerMediaLibrary {
	for index := range libraries {
		libraries[index].ArtworkSource = "fallback"
		libraries[index].ArtworkRevision = fallbackArtworkRevision(libraries[index].ArtworkURL)
		candidates, err := s.mediaLibraryCandidates(libraries[index].ID)
		if err != nil || len(candidates) == 0 {
			continue
		}
		asset, err := s.generate(ctx, libraries[index].Name, candidates)
		if err != nil {
			s.log.Debug().Str("module", "library_artwork").Str("library_kind", "media").Str("error_code", "library_artwork_generation_failed").Msg("动态媒体库封面生成失败，继续使用兜底图")
			continue
		}
		libraries[index].ArtworkURL = s.signedArtworkURL(asset.Digest)
		libraries[index].ArtworkRevision = asset.Digest
		libraries[index].ArtworkSource = "generated"
	}
	return libraries
}

func (s *LibraryArtworkService) DecoratePluginLibraries(ctx context.Context, actor Actor, libraries []PluginOnlineLibrarySummary) []PluginOnlineLibrarySummary {
	if s.plugins == nil || s.assets == nil {
		return libraries
	}
	for index := range libraries {
		libraries[index].ArtworkSource = "fallback"
		libraries[index].ArtworkRevision = fallbackArtworkRevision(libraries[index].ArtworkURL)
		items, err := s.plugins.OnlineArtworkCandidates(ctx, actor, libraries[index].ID)
		if err != nil || len(items) == 0 {
			continue
		}
		pluginID := libraries[index].PluginID
		connectionID := libraries[index].ConnectionID
		candidates := make([]artworkCandidate, 0, len(items))
		for _, item := range items {
			item := item
			candidates = append(candidates, artworkCandidate{
				key: item.ID,
				load: func(loadCtx context.Context) ([]byte, error) {
					stream, err := s.assets.OpenAssetForPluginConnection(loadCtx, pluginID, connectionID, item.AssetRef, http.MethodGet, "")
					if err != nil {
						return nil, err
					}
					defer stream.Body.Close()
					if stream.StatusCode != http.StatusOK {
						return nil, errors.New("plugin artwork asset returned non-200 status")
					}
					contentType := strings.ToLower(strings.TrimSpace(strings.Split(stream.Header.Get("Content-Type"), ";")[0]))
					if contentType != "image/jpeg" && contentType != "image/png" {
						return nil, errors.New("plugin artwork asset type is unsupported")
					}
					return readBoundedArtwork(stream.Body)
				},
			})
		}
		asset, err := s.generate(ctx, libraries[index].Name, candidates)
		if err != nil {
			s.log.Debug().Str("module", "library_artwork").Str("library_kind", "plugin").Str("plugin_id", pluginID).Str("error_code", "library_artwork_generation_failed").Msg("插件动态媒体库封面生成失败，继续使用兜底图")
			continue
		}
		libraries[index].ArtworkURL = s.signedArtworkURL(asset.Digest)
		libraries[index].ArtworkRevision = asset.Digest
		libraries[index].ArtworkSource = "generated"
	}
	return libraries
}

func (s *LibraryArtworkService) Open(digest string) (LibraryArtworkAsset, error) {
	if len(digest) != sha256.Size*2 {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	s.mu.RLock()
	data, ok := s.cache[digest]
	s.mu.RUnlock()
	if !ok {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	return LibraryArtworkAsset{Digest: digest, Bytes: append([]byte(nil), data...)}, nil
}

func (s *LibraryArtworkService) OpenSigned(digest, expiration, signature string) (LibraryArtworkAsset, error) {
	if len(expiration) == 0 || len(expiration) > 20 || len(signature) != 43 {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	expiresAt, err := strconv.ParseInt(expiration, 10, 64)
	if err != nil || expiresAt < s.currentTime().Unix() {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	expected := s.artworkSignature(digest, expiration)
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return LibraryArtworkAsset{}, appError(CodeNotFound, "媒体库封面不存在", nil)
	}
	return s.Open(digest)
}

func (s *LibraryArtworkService) mediaLibraryCandidates(libraryID uint) ([]artworkCandidate, error) {
	if s.metadata == nil {
		return nil, nil
	}
	client, err := s.metadata.Client()
	if err != nil {
		return nil, nil
	}
	var rows []models.MediaLibraryRecognition
	err = s.db.Model(&models.MediaLibraryRecognition{}).
		Where("media_library_recognitions.library_id = ? AND media_library_recognitions.status = ?", libraryID, "matched").
		Where("EXISTS (SELECT 1 FROM media_library_entries WHERE media_library_entries.library_id = ? AND media_library_entries.recognition_id = media_library_recognitions.id)", libraryID).
		Order("media_library_recognitions.updated_at DESC, media_library_recognitions.id DESC").
		Limit(64).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	seenRecognition := make(map[uint]struct{}, len(rows))
	seenImage := make(map[string]struct{}, len(rows))
	candidates := make([]artworkCandidate, 0, libraryArtworkCandidateLimit)
	for _, row := range rows {
		if _, exists := seenRecognition[row.ID]; exists {
			continue
		}
		seenRecognition[row.ID] = struct{}{}
		_, snapshot, decodeErr := decodeRecognitionMetadata(row.MetadataJSON)
		if decodeErr != nil {
			continue
		}
		path := safeTMDBImagePath(snapshot.PosterPath)
		if path == "" {
			path = safeTMDBImagePath(snapshot.BackdropPath)
		}
		if path == "" {
			continue
		}
		if _, exists := seenImage[path]; exists {
			continue
		}
		seenImage[path] = struct{}{}
		imagePath := path
		candidates = append(candidates, artworkCandidate{
			key: "tmdb:" + imagePath,
			load: func(loadCtx context.Context) ([]byte, error) {
				return client.DownloadJPEG(loadCtx, imagePath, "w500", libraryArtworkMaxSourceBytes)
			},
		})
		if len(candidates) == libraryArtworkCandidateLimit {
			break
		}
	}
	return candidates, nil
}

func (s *LibraryArtworkService) generate(ctx context.Context, title string, candidates []artworkCandidate) (LibraryArtworkAsset, error) {
	candidates = normalizeArtworkCandidates(candidates)
	if len(candidates) == 0 {
		return LibraryArtworkAsset{}, errors.New("library artwork has no candidates")
	}
	generationKey := artworkGenerationKey(title, candidates)
	s.mu.RLock()
	cachedDigest := s.generation[generationKey]
	s.mu.RUnlock()
	if cachedDigest != "" {
		if cached, err := s.Open(cachedDigest); err == nil {
			return cached, nil
		}
	}
	images := make([]image.Image, 0, libraryArtworkRenderLimit)
	for _, candidate := range candidates {
		data, err := candidate.load(ctx)
		if err != nil {
			continue
		}
		decoded, err := decodeLibraryArtwork(data)
		if err != nil {
			continue
		}
		images = append(images, decoded)
		if len(images) == libraryArtworkRenderLimit {
			break
		}
	}
	if len(images) == 0 {
		return LibraryArtworkAsset{}, errors.New("library artwork candidates are unavailable")
	}
	rendered := renderLibraryArtwork(images)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, rendered, &jpeg.Options{Quality: 88}); err != nil {
		return LibraryArtworkAsset{}, err
	}
	data := encoded.Bytes()
	contentHash := sha256.Sum256(data)
	digest := hex.EncodeToString(contentHash[:])
	s.mu.Lock()
	if s.cache == nil {
		s.cache = make(map[string][]byte)
	}
	if s.generation == nil {
		s.generation = make(map[string]string)
	}
	if _, exists := s.cache[digest]; !exists {
		if len(s.order) >= libraryArtworkCacheLimit {
			evicted := s.order[0]
			delete(s.cache, evicted)
			for key, cachedDigest := range s.generation {
				if cachedDigest == evicted {
					delete(s.generation, key)
				}
			}
			s.order = s.order[1:]
		}
		s.cache[digest] = append([]byte(nil), data...)
		s.order = append(s.order, digest)
	}
	s.generation[generationKey] = digest
	s.mu.Unlock()
	return LibraryArtworkAsset{Digest: digest, Bytes: append([]byte(nil), data...)}, nil
}

func decodeLibraryArtwork(data []byte) (image.Image, error) {
	configuration, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || configuration.Width < 32 || configuration.Height < 32 ||
		configuration.Width > libraryArtworkMaxDimension || configuration.Height > libraryArtworkMaxDimension ||
		int64(configuration.Width)*int64(configuration.Height) > libraryArtworkMaxPixels {
		return nil, errors.New("library artwork dimensions are invalid")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("library artwork image is invalid")
	}
	return decoded, nil
}

func normalizeArtworkCandidates(input []artworkCandidate) []artworkCandidate {
	result := make([]artworkCandidate, 0, min(len(input), libraryArtworkCandidateLimit))
	seen := make(map[string]struct{}, len(input))
	for _, candidate := range input {
		candidate.key = strings.TrimSpace(candidate.key)
		if candidate.key == "" || len(candidate.key) > 512 || candidate.load == nil {
			continue
		}
		if _, exists := seen[candidate.key]; exists {
			continue
		}
		seen[candidate.key] = struct{}{}
		result = append(result, candidate)
		if len(result) == libraryArtworkCandidateLimit {
			break
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].key < result[right].key })
	return result
}

func artworkGenerationKey(title string, candidates []artworkCandidate) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, libraryArtworkTemplateVersion+"\x00"+strings.TrimSpace(title)+"\x00")
	for _, candidate := range candidates {
		_, _ = io.WriteString(hash, candidate.key+"\x00")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func renderLibraryArtwork(images []image.Image) *image.RGBA {
	destination := image.NewRGBA(image.Rect(0, 0, libraryArtworkWidth, libraryArtworkHeight))
	count := len(images)
	for index, source := range images {
		left := index * libraryArtworkWidth / count
		right := (index + 1) * libraryArtworkWidth / count
		drawCover(destination, image.Rect(left, 0, right, libraryArtworkHeight), source)
	}
	for y := 0; y < libraryArtworkHeight; y++ {
		position := float64(y) / float64(libraryArtworkHeight-1)
		alpha := uint8(10 + 105*position*position)
		for x := 0; x < libraryArtworkWidth; x++ {
			base := destination.RGBAAt(x, y)
			destination.SetRGBA(x, y, blendOver(base, color.RGBA{A: alpha}))
		}
	}
	return destination
}

func drawCover(destination *image.RGBA, target image.Rectangle, source image.Image) {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := target.Dx(), target.Dy()
	if sourceWidth <= 0 || sourceHeight <= 0 || targetWidth <= 0 || targetHeight <= 0 {
		return
	}
	scaleX := float64(sourceWidth) / float64(targetWidth)
	scaleY := float64(sourceHeight) / float64(targetHeight)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}
	visibleWidth := float64(targetWidth) * scale
	visibleHeight := float64(targetHeight) * scale
	offsetX := float64(bounds.Min.X) + (float64(sourceWidth)-visibleWidth)/2
	offsetY := float64(bounds.Min.Y) + (float64(sourceHeight)-visibleHeight)/2
	for y := target.Min.Y; y < target.Max.Y; y++ {
		sourceY := int(offsetY + (float64(y-target.Min.Y)+0.5)*scale)
		if sourceY >= bounds.Max.Y {
			sourceY = bounds.Max.Y - 1
		}
		for x := target.Min.X; x < target.Max.X; x++ {
			sourceX := int(offsetX + (float64(x-target.Min.X)+0.5)*scale)
			if sourceX >= bounds.Max.X {
				sourceX = bounds.Max.X - 1
			}
			destination.Set(x, y, source.At(sourceX, sourceY))
		}
	}
}

func blendOver(base, overlay color.RGBA) color.RGBA {
	alpha := uint16(overlay.A)
	inverse := uint16(255 - overlay.A)
	return color.RGBA{
		R: uint8((uint16(overlay.R)*alpha + uint16(base.R)*inverse) / 255),
		G: uint8((uint16(overlay.G)*alpha + uint16(base.G)*inverse) / 255),
		B: uint8((uint16(overlay.B)*alpha + uint16(base.B)*inverse) / 255),
		A: 255,
	}
}

func readBoundedArtwork(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, libraryArtworkMaxSourceBytes+1))
	if err != nil || len(data) == 0 || len(data) > libraryArtworkMaxSourceBytes {
		return nil, errors.New("library artwork asset exceeds the safe limit")
	}
	return data, nil
}

func (s *LibraryArtworkService) signedArtworkURL(digest string) string {
	// Quantize tickets so repeated library-list requests reuse the same URL and
	// do not force Player's image cache to redownload identical content.
	ttlSeconds := int64(libraryArtworkTicketTTL / time.Second)
	expiresAt := (s.currentTime().Unix()/ttlSeconds + 2) * ttlSeconds
	expiration := strconv.FormatInt(expiresAt, 10)
	return "/api/v1/assets/generated-library-covers/" + digest + "?exp=" + expiration + "&sig=" + s.artworkSignature(digest, expiration)
}

func (s *LibraryArtworkService) artworkSignature(digest, expiration string) string {
	mac := hmac.New(sha256.New, s.signingKey[:])
	_, _ = io.WriteString(mac, "GET\n/api/v1/assets/generated-library-covers/"+digest+"\n"+expiration)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *LibraryArtworkService) currentTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func fallbackArtworkRevision(value string) string {
	hash := sha256.Sum256([]byte("fallback\x00" + value))
	return hex.EncodeToString(hash[:])
}
