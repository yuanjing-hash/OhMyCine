package services

import (
	"context"
	"errors"
	"strings"

	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

const maxCloudBoundaryDepth = 128

func providerItemWithinRoot(ctx context.Context, driver cloudpkg.Driver, itemID, rootID string) (cloudpkg.Item, error) {
	itemID, rootID = strings.TrimSpace(itemID), strings.TrimSpace(rootID)
	if itemID == "" || rootID == "" {
		return cloudpkg.Item{}, errors.New("provider item boundary is incomplete")
	}
	current := itemID
	visited := make(map[string]struct{}, maxCloudBoundaryDepth)
	var initial cloudpkg.Item
	for depth := 0; depth < maxCloudBoundaryDepth; depth++ {
		item, err := driver.Stat(ctx, current)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		if strings.TrimSpace(item.ID) != current {
			return cloudpkg.Item{}, errors.New("provider returned a mismatched item identity")
		}
		if depth == 0 {
			initial = item
		}
		if current == rootID {
			return initial, nil
		}
		if _, exists := visited[current]; exists {
			return cloudpkg.Item{}, errors.New("provider item parent cycle")
		}
		visited[current] = struct{}{}
		current = strings.TrimSpace(item.ParentID)
		if current == "" || (current == "0" && rootID != "0") {
			return cloudpkg.Item{}, errors.New("provider item is outside the configured root")
		}
	}
	return cloudpkg.Item{}, errors.New("provider item parent depth exceeded")
}

// providerDirectoryWithin distinguishes a proven non-descendant from a
// provider failure so overlap validation cannot silently pass during outages.
func providerDirectoryWithin(ctx context.Context, driver cloudpkg.Driver, itemID, rootID string) (bool, error) {
	itemID, rootID = strings.TrimSpace(itemID), strings.TrimSpace(rootID)
	if itemID == "" || rootID == "" {
		return false, errors.New("provider directory boundary is incomplete")
	}
	current := itemID
	visited := make(map[string]struct{}, maxCloudBoundaryDepth)
	for depth := 0; depth < maxCloudBoundaryDepth; depth++ {
		item, err := driver.Stat(ctx, current)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(item.ID) != current || !item.IsDir {
			return false, errors.New("provider returned an invalid directory identity")
		}
		if current == rootID {
			return true, nil
		}
		if _, exists := visited[current]; exists {
			return false, errors.New("provider directory parent cycle")
		}
		visited[current] = struct{}{}
		current = strings.TrimSpace(item.ParentID)
		if current == "" || current == "0" {
			return rootID == "0" && current == rootID, nil
		}
	}
	return false, errors.New("provider directory parent depth exceeded")
}
