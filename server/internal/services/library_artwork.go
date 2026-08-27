package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	"gorm.io/gorm"
)

const (
	libraryArtworkTemplateVersion = "library-artwork-style-static-3-v2"
	libraryArtworkWidth           = 1920
	libraryArtworkHeight          = 1080
	libraryArtworkCandidateLimit  = 9
	libraryArtworkRenderLimit     = 9
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

func (s *LibraryArtworkService) DecorateMediaCategories(ctx context.Context, libraryID uint, categories []PlayerMediaCategory) []PlayerMediaCategory {
	for index := range categories {
		categories[index].ArtworkSource = "fallback"
		categories[index].ArtworkRevision = fallbackArtworkRevision(categories[index].ArtworkURL)
		candidates, err := s.mediaCategoryCandidates(libraryID, categories[index].Name, categories[index].MediaType)
		if err != nil || len(candidates) == 0 {
			continue
		}
		asset, err := s.generate(ctx, categories[index].Name, candidates)
		if err != nil {
			s.log.Debug().Str("module", "library_artwork").Uint("library_id", libraryID).Str("category", categories[index].Name).Str("error_code", "library_artwork_generation_failed").Msg("动态分类封面生成失败，继续使用兜底图")
			continue
		}
		categories[index].ArtworkURL = s.signedArtworkURL(asset.Digest)
		categories[index].ArtworkRevision = asset.Digest
		categories[index].ArtworkSource = "generated"
	}
	return categories
}

func (s *LibraryArtworkService) DecoratePluginNavigation(ctx context.Context, actor Actor, libraryID string, raw json.RawMessage) (json.RawMessage, error) {
	if s.plugins == nil || s.assets == nil {
		return raw, nil
	}
	var response pluginNavigationResponse
	if err := json.Unmarshal(raw, &response); err != nil || response.Version != 2 || response.Mode != "hierarchical" {
		return raw, nil
	}
	_, connection, manifest, err := s.plugins.onlineLibrary(libraryID)
	if err != nil {
		return raw, nil
	}
	fallbackURL := ""
	if manifest.LibraryArtwork != "" {
		fallbackURL = "/api/v1/assets/plugin-covers/" + manifest.PackageSHA256
	}
	for index := range response.Nodes {
		response.Nodes[index].ArtworkURL = fallbackURL
		response.Nodes[index].ArtworkRevision = fallbackArtworkRevision(fallbackURL)
		response.Nodes[index].ArtworkSource = "fallback"
		scopeKey := "route:" + response.Nodes[index].RouteKey
		if response.Nodes[index].Kind == "branch" {
			claim, verifyErr := s.plugins.verifyPluginNavigationToken(response.Nodes[index].NodeToken)
			if verifyErr != nil || claim.LibraryID != libraryID {
				continue
			}
			scopeKey = "branch:" + claim.NodeKey
		}
		items, candidateErr := s.plugins.OnlineArtworkCandidates(ctx, actor, libraryID, scopeKey)
		if candidateErr != nil || len(items) == 0 {
			continue
		}
		candidates := s.pluginArtworkCandidates(manifest.ID, connection.ID, scopeKey, items)
		asset, generateErr := s.generate(ctx, response.Nodes[index].Title, candidates)
		if generateErr != nil {
			s.log.Debug().Str("module", "library_artwork").Str("plugin_id", manifest.ID).Str("scope", scopeKey).Str("error_code", "library_artwork_generation_failed").Msg("插件分类封面生成失败，继续使用兜底图")
			continue
		}
		response.Nodes[index].ArtworkURL = s.signedArtworkURL(asset.Digest)
		response.Nodes[index].ArtworkRevision = asset.Digest
		response.Nodes[index].ArtworkSource = "generated"
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return raw, nil
	}
	return json.RawMessage(encoded), nil
}

func (s *LibraryArtworkService) pluginArtworkCandidates(pluginID, connectionID, scopeKey string, items []contract.LibraryArtworkCandidate) []artworkCandidate {
	candidates := make([]artworkCandidate, 0, len(items))
	for _, item := range items {
		item := item
		candidates = append(candidates, artworkCandidate{
			key: "plugin:" + scopeKey + ":" + item.ID,
			load: func(loadCtx context.Context) ([]byte, error) {
				stream, err := s.assets.OpenAssetForPluginConnection(loadCtx, pluginID, connectionID, item.AssetRef, http.MethodGet, "")
				if err != nil {
					return nil, err
				}
				defer func() { _ = stream.Body.Close() }()
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
	return candidates
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

func (s *LibraryArtworkService) mediaCategoryCandidates(libraryID uint, categoryName, mediaType string) ([]artworkCandidate, error) {
	if s.metadata == nil {
		return nil, nil
	}
	categoryName = strings.TrimSpace(categoryName)
	entryMediaType := "movie"
	if mediaType == "series" {
		entryMediaType = "tv"
	} else if mediaType != "movie" {
		return nil, nil
	}
	client, err := s.metadata.Client()
	if err != nil {
		return nil, nil
	}
	var rows []models.MediaLibraryRecognition
	err = s.db.Model(&models.MediaLibraryRecognition{}).
		Where("media_library_recognitions.library_id = ?", libraryID).
		Where("EXISTS (SELECT 1 FROM media_library_entries WHERE media_library_entries.library_id = ? AND media_library_entries.recognition_id = media_library_recognitions.id AND media_library_entries.category_name = ? AND media_library_entries.media_type = ?)", libraryID, categoryName, entryMediaType).
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
	if len(images) == 0 {
		return image.NewRGBA(image.Rect(0, 0, libraryArtworkWidth, libraryArtworkHeight))
	}
	images = fillStyle3PosterSlots(images, libraryArtworkRenderLimit)
	images = style3PosterOrder(images)
	theme := artworkThemeColor(images[0])
	destination := style3Gradient(theme)
	const (
		rows          = 3
		columns       = 3
		cellWidth     = 410
		cellHeight    = 610
		margin        = 22
		cornerRadius  = 46
		shadowExtra   = 60
		startX        = 835
		startY        = -362
		columnSpacing = 100
		rotationAngle = -15.8
	)
	columnHeight := rows*cellHeight + (rows-1)*margin
	for columnIndex := 0; columnIndex < columns; columnIndex++ {
		start := columnIndex * rows
		if start >= len(images) {
			break
		}
		column := image.NewRGBA(image.Rect(0, 0, cellWidth+shadowExtra, columnHeight+shadowExtra))
		for rowIndex := 0; rowIndex < rows && start+rowIndex < len(images); rowIndex++ {
			poster := style3Poster(images[start+rowIndex], cellWidth, cellHeight, cornerRadius, shadowExtra)
			pasteRGBA(column, poster, image.Pt(0, rowIndex*(cellHeight+margin)))
		}
		rotated := rotateRGBA(column, rotationAngle)
		columnX := startX + columnIndex*columnSpacing
		columnCenterY := startY + columnHeight/2
		columnCenterX := columnX
		switch columnIndex {
		case 1:
			columnCenterX += cellWidth - 50
		case 2:
			columnCenterY -= 155
			columnCenterX += (cellWidth-50)*2 + 40
		}
		finalX := columnCenterX - rotated.Bounds().Dx()/2 + cellWidth/2
		finalY := columnCenterY - rotated.Bounds().Dy()/2
		pasteRGBA(destination, rotated, image.Pt(finalX, finalY))
	}
	return destination
}

func fillStyle3PosterSlots(images []image.Image, target int) []image.Image {
	if len(images) == 0 || target <= 0 {
		return nil
	}
	if target > libraryArtworkRenderLimit {
		target = libraryArtworkRenderLimit
	}
	result := make([]image.Image, target)
	for index := range result {
		result[index] = images[index%len(images)]
	}
	return result
}

func style3PosterOrder(images []image.Image) []image.Image {
	// Mirrors style_static_3.py's 315426987 placement so the first two
	// candidates occupy the most visible center positions.
	order := [...]int{2, 0, 4, 3, 1, 5, 8, 7, 6}
	result := make([]image.Image, 0, len(images))
	used := make([]bool, len(images))
	for _, index := range order {
		if index < len(images) {
			result = append(result, images[index])
			used[index] = true
		}
	}
	for index, source := range images {
		if !used[index] {
			result = append(result, source)
		}
	}
	return result
}

func style3Gradient(theme color.RGBA) *image.RGBA {
	destination := image.NewRGBA(image.Rect(0, 0, libraryArtworkWidth, libraryArtworkHeight))
	left := color.RGBA{
		R: uint8(float64(theme.R) * .65),
		G: uint8(float64(theme.G) * .65),
		B: uint8(float64(theme.B) * .65),
		A: 255,
	}
	lighten := func(value uint8) uint8 {
		return uint8(math.Min(230, math.Max(float64(value)*1.9, float64(value)+80)))
	}
	right := color.RGBA{R: lighten(left.R), G: lighten(left.G), B: lighten(left.B), A: 255}
	for x := 0; x < libraryArtworkWidth; x++ {
		position := float64(x) / float64(libraryArtworkWidth-1)
		// Match style_static_3.py's left-to-right nonlinear mask.
		eased := math.Pow(position, .7)
		pixel := color.RGBA{
			R: uint8(float64(left.R)*(1-eased) + float64(right.R)*eased),
			G: uint8(float64(left.G)*(1-eased) + float64(right.G)*eased),
			B: uint8(float64(left.B)*(1-eased) + float64(right.B)*eased), A: 255,
		}
		for y := 0; y < libraryArtworkHeight; y++ {
			destination.SetRGBA(x, y, pixel)
		}
	}
	return destination
}

func artworkThemeColor(source image.Image) color.RGBA {
	bounds := source.Bounds()
	best := color.RGBA{R: 237, G: 159, B: 77, A: 255}
	bestScore := -1.0
	for y := bounds.Min.Y; y < bounds.Max.Y; y += max(1, bounds.Dy()/24) {
		for x := bounds.Min.X; x < bounds.Max.X; x += max(1, bounds.Dx()/24) {
			r, g, b, _ := source.At(x, y).RGBA()
			rf, gf, bf := float64(r>>8), float64(g>>8), float64(b>>8)
			maximum, minimum := math.Max(rf, math.Max(gf, bf)), math.Min(rf, math.Min(gf, bf))
			lightness := (maximum + minimum) / 2
			saturation := maximum - minimum
			score := saturation - math.Abs(lightness-140)*.25
			if lightness >= 55 && lightness <= 215 && score > bestScore {
				best, bestScore = color.RGBA{R: uint8(rf), G: uint8(gf), B: uint8(bf), A: 255}, score
			}
		}
	}
	return best
}

func style3Poster(source image.Image, width, height, radius, shadowExtra int) *image.RGBA {
	poster := image.NewRGBA(image.Rect(0, 0, width, height))
	drawCover(poster, poster.Bounds(), source)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if !insideRoundedRectangle(x, y, width, height, radius) {
				poster.SetRGBA(x, y, color.RGBA{})
			}
		}
	}
	tile := image.NewRGBA(image.Rect(0, 0, width+shadowExtra, height+shadowExtra))
	for layer := 0; layer < 20; layer++ {
		offset := 20 - layer/2
		alpha := uint8(9 + layer/2)
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if insideRoundedRectangle(x, y, width, height, radius) {
					blendRGBAAt(tile, x+offset, y+offset, color.RGBA{A: alpha})
				}
			}
		}
	}
	pasteRGBA(tile, poster, image.Point{})
	return tile
}

func insideRoundedRectangle(x, y, width, height, radius int) bool {
	if x >= radius && x < width-radius || y >= radius && y < height-radius {
		return true
	}
	cx := radius
	if x >= width-radius {
		cx = width - radius - 1
	}
	cy := radius
	if y >= height-radius {
		cy = height - radius - 1
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

func rotateRGBA(source *image.RGBA, degrees float64) *image.RGBA {
	angle := degrees * math.Pi / 180
	cosine, sine := math.Cos(angle), math.Sin(angle)
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	rotatedWidth := int(math.Ceil(math.Abs(float64(width)*cosine) + math.Abs(float64(height)*sine)))
	rotatedHeight := int(math.Ceil(math.Abs(float64(width)*sine) + math.Abs(float64(height)*cosine)))
	destination := image.NewRGBA(image.Rect(0, 0, rotatedWidth, rotatedHeight))
	sourceCX, sourceCY := float64(width-1)/2, float64(height-1)/2
	destinationCX, destinationCY := float64(rotatedWidth-1)/2, float64(rotatedHeight-1)/2
	for y := 0; y < rotatedHeight; y++ {
		for x := 0; x < rotatedWidth; x++ {
			dx, dy := float64(x)-destinationCX, float64(y)-destinationCY
			sourceX := cosine*dx + sine*dy + sourceCX
			sourceY := -sine*dx + cosine*dy + sourceCY
			sx, sy := int(math.Round(sourceX)), int(math.Round(sourceY))
			if sx >= 0 && sx < width && sy >= 0 && sy < height {
				destination.SetRGBA(x, y, source.RGBAAt(sx, sy))
			}
		}
	}
	return destination
}

func pasteRGBA(destination, source *image.RGBA, point image.Point) {
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			pixel := source.RGBAAt(x, y)
			if pixel.A != 0 {
				blendRGBAAt(destination, point.X+x, point.Y+y, pixel)
			}
		}
	}
}

func blendRGBAAt(destination *image.RGBA, x, y int, overlay color.RGBA) {
	if !image.Pt(x, y).In(destination.Bounds()) || overlay.A == 0 {
		return
	}
	destination.SetRGBA(x, y, blendOver(destination.RGBAAt(x, y), overlay))
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
	if overlay.A == 255 || base.A == 0 {
		return overlay
	}
	if overlay.A == 0 {
		return base
	}
	overlayAlpha := uint32(overlay.A)
	baseAlpha := uint32(base.A)
	inverse := uint32(255 - overlay.A)
	outAlpha := overlayAlpha + (baseAlpha*inverse)/255
	if outAlpha == 0 {
		return color.RGBA{}
	}
	blend := func(over, under uint8) uint8 {
		value := (uint32(over)*overlayAlpha*255 + uint32(under)*baseAlpha*inverse) / (outAlpha * 255)
		return uint8(value)
	}
	return color.RGBA{
		R: blend(overlay.R, base.R), G: blend(overlay.G, base.G), B: blend(overlay.B, base.B), A: uint8(outAlpha),
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
