package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/builtin"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type MediaIdentitySearchInput struct {
	MediaType string
	TMDBID    int64
	Season    *int
	Page      int
	SiteID    *uint
	SiteIDs   []uint
}

type MediaIdentitySearchResult struct {
	MediaType  string            `json:"media_type"`
	TMDBID     int64             `json:"tmdb_id"`
	Title      string            `json:"title"`
	Year       *int              `json:"year,omitempty"`
	QueryNames []tmdb.SearchName `json:"query_names"`
	Groups     []SiteSearchGroup `json:"groups"`
}

// SearchMediaIdentity verifies a stable TMDB identity and aggregates the same
// bounded multilingual name set across configured sites. The browser receives
// only safe display names and ordinary actor-bound opaque result tokens.
func (s *SiteService) SearchMediaIdentity(ctx context.Context, actor Actor, input MediaIdentitySearchInput) (MediaIdentitySearchResult, error) {
	result := MediaIdentitySearchResult{}
	groups := []SiteSearchGroup{}
	err := s.SearchMediaIdentityEach(ctx, actor, input, func(metadata MediaIdentitySearchResult) {
		result = metadata
	}, func(group SiteSearchGroup) {
		groups = append(groups, group)
	})
	if err != nil {
		return MediaIdentitySearchResult{}, err
	}
	priorities := s.sitePriorityMap(input.SiteID, input.SiteIDs)
	sort.SliceStable(groups, func(left, right int) bool {
		leftPriority, rightPriority := priorities[groups[left].SiteID], priorities[groups[right].SiteID]
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return groups[left].SiteID < groups[right].SiteID
	})
	result.Groups = groups
	return result, nil
}

// SearchMediaIdentityEach shares the same preflight and per-site aggregation
// with the JSON endpoint while allowing SSE callers to receive one complete
// site group as soon as that site finishes all bounded aliases. Callbacks are
// serialized, so HTTP writers never receive concurrent writes.
func (s *SiteService) SearchMediaIdentityEach(ctx context.Context, actor Actor, input MediaIdentitySearchInput, emitMetadata func(MediaIdentitySearchResult), emit func(SiteSearchGroup)) error {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return appError(CodePermissionDenied, "无权搜索种子资源", nil)
	}
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	if input.TMDBID <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") || s.metadata == nil {
		return appError(CodeInvalidRequest, "TMDB 搜索身份无效", nil)
	}
	if input.Season != nil && (input.MediaType != "tv" || *input.Season < 0 || *input.Season > 200) {
		return appError(CodeInvalidRequest, "资源搜索季数无效", nil)
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Page < 1 || input.Page > 20 {
		return appError(CodeInvalidRequest, "种子资源搜索页码无效", nil)
	}
	client, err := s.metadata.Client()
	if err != nil {
		return appError(CodeTMDBUnavailable, "TMDB 详情服务暂时不可用", nil)
	}
	verified, names, err := client.IdentitySearchNames(ctx, input.MediaType, input.TMDBID, "zh-CN", tmdb.DefaultIdentitySearchNameLimit)
	if err != nil {
		return appError(tmdb.ErrorCode(err), "TMDB 作品身份无法解析", nil)
	}
	if len(names) == 0 {
		return appError(CodeTMDBUnavailable, "TMDB 作品没有可用搜索名称", nil)
	}
	metadata := MediaIdentitySearchResult{MediaType: input.MediaType, TMDBID: input.TMDBID, Title: verified.Title, Year: cloneInt(verified.ReleaseYear), QueryNames: names, Groups: []SiteSearchGroup{}}
	records, err := s.searchSiteRecords(input.SiteID, input.SiteIDs)
	if err != nil {
		return err
	}
	if emitMetadata != nil {
		emitMetadata(metadata)
	}
	semaphore := make(chan struct{}, 4)
	var wait sync.WaitGroup
	var emitMu sync.Mutex
	for _, record := range records {
		record := record
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			group := s.searchMediaIdentitySite(ctx, actor, input, verified, names, record)
			if ctx.Err() != nil || emit == nil {
				return
			}
			emitMu.Lock()
			emit(group)
			emitMu.Unlock()
		}()
	}
	wait.Wait()
	if ctx.Err() != nil {
		return appError(CodeSiteUnavailable, "种子资源搜索已取消", nil)
	}
	return nil
}

func (s *SiteService) searchMediaIdentitySite(ctx context.Context, actor Actor, input MediaIdentitySearchInput, verified tmdb.Match, names []tmdb.SearchName, record models.Site) SiteSearchGroup {
	var target *SiteSearchGroup
	privateKeys := make(map[string]struct{})
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		group := s.searchSite(ctx, actor, record, SiteSearchInput{Keyword: name.Value, MediaType: input.MediaType, SearchBy: "title", Year: verified.ReleaseYear, TMDBID: &input.TMDBID, Page: input.Page, SiteID: &record.ID})
		if target == nil {
			copy := group
			copy.Items = []SiteSearchResult{}
			copy.Skipped = 0
			copy.HasNext = false
			target = &copy
		}
		target.HasNext = target.HasNext || group.HasNext
		target.Skipped += group.Skipped
		if group.Status == "success" {
			target.Status, target.ErrorCode = "success", ""
		} else {
			target.ErrorCount++
			if target.Status != "success" {
				target.Status, target.ErrorCode = group.Status, group.ErrorCode
			}
		}
		for _, item := range group.Items {
			claim, claimErr := s.resolveAvailableClaim(item.Token, actor.User.ID)
			if claimErr != nil {
				continue
			}
			if !mediaIdentityResultMatches(item.Title, names, verified.ReleaseYear, input.Season) {
				s.deleteClaim(item.Token)
				target.Skipped++
				continue
			}
			key := strconv.FormatUint(uint64(group.SiteID), 10) + ":" + claim.TorrentID
			if _, duplicate := privateKeys[key]; duplicate {
				s.deleteClaim(item.Token)
				continue
			}
			if bindErr := s.bindClaimRecognition(item.Token, actor.User.ID, input.TMDBID, input.MediaType, mediaIdentitySourceDirectID, mediaIdentityStatusVerified, false); bindErr != nil {
				s.deleteClaim(item.Token)
				continue
			}
			privateKeys[key] = struct{}{}
			item.MatchedName = name.Value
			item.ResourceFingerprint = privateResultFingerprint(group.SiteID, claim.TorrentID)
			target.Items = append(target.Items, item)
		}
	}
	if target == nil {
		definition, _ := builtin.DefinitionForKey(record.Kind)
		return SiteSearchGroup{SiteID: record.ID, SiteName: record.Name, SiteType: definition.SiteType, Status: "error", ErrorCode: CodeSiteUnavailable, Page: input.Page, Items: []SiteSearchResult{}}
	}
	sort.SliceStable(target.Items, func(left, right int) bool {
		leftSeeders, rightSeeders := intValue(target.Items[left].Seeders), intValue(target.Items[right].Seeders)
		if leftSeeders != rightSeeders {
			return leftSeeders > rightSeeders
		}
		if compared := compareOptionalTime(target.Items[left].Published, target.Items[right].Published); compared != 0 {
			return compared > 0
		}
		if target.Items[left].SizeBytes != target.Items[right].SizeBytes {
			return target.Items[left].SizeBytes > target.Items[right].SizeBytes
		}
		return target.Items[left].Title < target.Items[right].Title
	})
	return *target
}

func privateResultFingerprint(siteID uint, torrentID string) string {
	sum := sha256.Sum256([]byte(strconv.FormatUint(uint64(siteID), 10) + "\x00" + torrentID))
	return hex.EncodeToString(sum[:])
}

func mediaIdentityResultMatches(title string, names []tmdb.SearchName, year, season *int) bool {
	parsed, err := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: title, SourceKind: mediarecognition.SourceDownload})
	if err != nil {
		return false
	}
	candidate := identitySearchKey(parsed.CanonicalTitle)
	matched := false
	for _, name := range names {
		key := identitySearchKey(name.Value)
		if key != "" && (candidate == key || strings.Contains(candidate, key) || strings.Contains(key, candidate)) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if year != nil && parsed.Year != nil && absInt(*year-*parsed.Year) > 1 {
		return false
	}
	return season == nil || parsed.Season != nil && *season == *parsed.Season
}

func identitySearchKey(value string) string {
	value = cases.Fold().String(norm.NFKC.String(strings.TrimSpace(value)))
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return character
		}
		return -1
	}, value)
}

func (s *SiteService) deleteClaim(token string) {
	s.vaultMu.Lock()
	delete(s.vault, token)
	s.vaultMu.Unlock()
}

func (s *SiteService) sitePriorityMap(siteID *uint, siteIDs []uint) map[uint]int {
	var records []models.Site
	query := s.db.Select("id", "priority")
	if siteID != nil {
		query = query.Where("id = ?", *siteID)
	} else if len(siteIDs) > 0 {
		query = query.Where("id IN ?", siteIDs)
	}
	_ = query.Find(&records).Error
	result := make(map[uint]int, len(records))
	for _, record := range records {
		result[record.ID] = record.Priority
	}
	return result
}

func intValue(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func compareOptionalTime(left, right *time.Time) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	if left.UnixNano() < right.UnixNano() {
		return -1
	}
	if left.UnixNano() > right.UnixNano() {
		return 1
	}
	return 0
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
