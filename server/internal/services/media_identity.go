package services

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

const (
	mediaIdentitySourceManual           = "manual"
	mediaIdentitySourceDirectID         = "direct_id"
	mediaIdentitySourceAutomatic        = "automatic"
	mediaIdentitySourceAI               = "ai"
	mediaIdentitySourceLocalProvisional = "local_provisional"
)

type MediaIdentitySnapshot struct {
	Version    int                                `json:"version"`
	Revision   uint64                             `json:"revision"`
	Source     string                             `json:"source"`
	Status     string                             `json:"status"`
	Locked     bool                               `json:"locked"`
	TMDBID     *int64                             `json:"tmdb_id,omitempty"`
	MediaType  string                             `json:"media_type,omitempty"`
	Title      string                             `json:"title,omitempty"`
	Year       *int                               `json:"year,omitempty"`
	Season     *int                               `json:"season,omitempty"`
	Episode    *int                               `json:"episode,omitempty"`
	Category   string                             `json:"category,omitempty"`
	Confidence *float64                           `json:"confidence,omitempty"`
	Episodes   []mediarecognition.FileEpisodeFact `json:"episodes,omitempty"`
}

func validateTransferIdentitySnapshot(task models.DownloadTask) error {
	if task.IdentityRevision == 0 {
		return errors.New("transfer identity revision is missing")
	}
	snapshot, err := decodeMediaIdentity(task.IdentitySnapshotJSON)
	if err != nil || snapshot.Revision != task.IdentityRevision {
		return errors.New("transfer identity snapshot revision is invalid")
	}
	if snapshot.Source != task.IdentitySource || snapshot.Status != task.IdentityStatus || snapshot.Locked != task.IdentityLocked {
		return errors.New("transfer identity snapshot state is inconsistent")
	}
	if snapshot.TMDBID == nil || task.ScrapeTMDBID == nil || *snapshot.TMDBID != *task.ScrapeTMDBID || snapshot.MediaType != task.ScrapeMediaType || snapshot.Category != task.ScrapeCategory || snapshot.Title != task.ScrapeTitle {
		return errors.New("transfer identity snapshot projection is inconsistent")
	}
	switch snapshot.Source {
	case mediaIdentitySourceManual:
		if !snapshot.Locked || snapshot.Status != mediaIdentityStatusVerified {
			return errors.New("manual transfer identity is not locked")
		}
	case mediaIdentitySourceDirectID:
		if snapshot.Locked || (snapshot.Status != mediaIdentityStatusVerified && snapshot.Status != mediaIdentityStatusProvisional) {
			return errors.New("direct transfer identity state is invalid")
		}
	case mediaIdentitySourceAutomatic, mediaIdentitySourceAI:
		if snapshot.Locked || (snapshot.Status != mediaIdentityStatusVerified && snapshot.Status != mediaIdentityStatusProvisional) {
			return errors.New("automatic transfer identity state is invalid")
		}
	default:
		return errors.New("transfer identity source is invalid")
	}
	return nil
}

func buildDownloadIdentitySnapshot(task models.DownloadTask, match scrapeMatch, manifest downloadpkg.Manifest, source, status string, locked bool, revision uint64) (MediaIdentitySnapshot, string, error) {
	_ = task
	if revision == 0 {
		revision = 1
	}
	snapshot := MediaIdentitySnapshot{Version: 1, Revision: revision, Source: source, Status: status, Locked: locked, TMDBID: cloneInt64(match.TMDBID), MediaType: strings.TrimSpace(match.MediaType), Title: strings.TrimSpace(match.Title), Year: cloneInt(match.Year), Season: cloneInt(match.Season), Episode: cloneInt(match.Episode), Category: strings.TrimSpace(match.Category), Confidence: cloneFloat64(match.Confidence)}
	files := make([]mediarecognition.FileFact, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, mediarecognition.FileFact{RelativePath: normalizedManifestPath(file.RelativePath), Size: file.Size})
	}
	if snapshot.MediaType == "tv" {
		snapshot.Episodes = mediarecognition.ResolvePackageEpisodes(files, mediarecognition.MediaTypeTV).Files
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return snapshot, "", err
	}
	return snapshot, string(raw), nil
}
