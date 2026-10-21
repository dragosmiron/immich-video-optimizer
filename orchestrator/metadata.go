package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"immich-video-optimizer/immich"
	"immich-video-optimizer/logger"
)

func (p *Pipeline) renameAlbumToCompleted(albumID string) {
	client := p.Client()
	if albumID == "" || client == nil {
		return
	}
	timestamp := time.Now().Format("20060102-150405")
	completedName := fmt.Sprintf("Immich-Optimizer-Completed-%s", timestamp)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.UpdateAlbumNameWithContext(ctx, albumID, completedName); err != nil {
		logger.Error("Failed to rename tracking album %s to completed %s: %v", albumID, completedName, err)
	}
}

func (p *Pipeline) rollbackAsset(assetID string) {
	client := p.Client()
	if assetID == "" || client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.DeleteAssetWithContext(ctx, assetID); err != nil {
		logger.Error("Rollback failed for asset %s: %v", assetID, err)
	} else {
		logger.Info("Rollback: deleted orphaned asset %s", assetID)
	}
}

// transferAssetMetadata transfers album memberships, favorite flag, description, tags, and stack
// membership from the original asset to the newly uploaded replacement asset.
func (p *Pipeline) transferAssetMetadata(ctx context.Context, origAsset *immich.Asset, newAssetID string, origAlbums []immich.Album, isFavorite bool, trackingAlbumID string) error {
	client := p.Client()
	if client == nil {
		return fmt.Errorf("client unavailable")
	}
	var albumTransferErr error
	seenAlbums := make(map[string]bool)
	for _, alb := range origAlbums {
		if alb.ID == "" || alb.ID == trackingAlbumID || strings.HasPrefix(alb.AlbumName, "Immich-Optimizer-") || seenAlbums[alb.ID] {
			continue
		}
		seenAlbums[alb.ID] = true
		if err := client.AddAssetsToAlbumWithContext(ctx, alb.ID, []string{newAssetID}); err != nil {
			logger.Error("Failed to add new asset %s to original album %s (%s): %v", newAssetID, alb.AlbumName, alb.ID, err)
			albumTransferErr = err
			break
		}
	}

	// Transfer favorite status if original was favorited
	var favTransferErr error
	if albumTransferErr == nil && isFavorite {
		if err := client.SetAssetFavoriteWithContext(ctx, newAssetID, true); err != nil {
			logger.Error("Failed to transfer favorite status to new asset %s: %v", newAssetID, err)
			favTransferErr = err
		}
	}

	// Transfer description if original had one
	var descTransferErr error
	if albumTransferErr == nil && favTransferErr == nil && origAsset.Description != "" {
		if err := client.UpdateAssetDescriptionWithContext(ctx, newAssetID, origAsset.Description); err != nil {
			logger.Error("Failed to transfer description to new asset %s: %v", newAssetID, err)
			descTransferErr = err
		}
	}

	// Transfer tags
	var tagsTransferErr error
	if albumTransferErr == nil && favTransferErr == nil && descTransferErr == nil {
		for _, tag := range origAsset.Tags {
			if err := client.TagAssetWithContext(ctx, tag.ID, newAssetID); err != nil {
				tagsTransferErr = err
				break
			}
		}
	}

	// Transfer stack membership
	var stackTransferErr error
	if albumTransferErr == nil && favTransferErr == nil && descTransferErr == nil && tagsTransferErr == nil && origAsset.Stack != nil {
		stackTransferErr = client.CopyAssetStackWithContext(ctx, origAsset.ID, newAssetID)
		if stackTransferErr == nil && origAsset.Stack.PrimaryAssetID == origAsset.ID && origAsset.Stack.ID != "" {
			stackTransferErr = client.UpdateStackPrimaryAssetWithContext(ctx, origAsset.Stack.ID, newAssetID)
		}
	}

	if albumTransferErr != nil || favTransferErr != nil || descTransferErr != nil || tagsTransferErr != nil || stackTransferErr != nil {
		var failed []string
		if albumTransferErr != nil {
			failed = append(failed, "album membership")
		}
		if favTransferErr != nil {
			failed = append(failed, "favorite status")
		}
		if descTransferErr != nil {
			failed = append(failed, "description")
		}
		if tagsTransferErr != nil {
			failed = append(failed, "tags")
		}
		if stackTransferErr != nil {
			failed = append(failed, "stack membership")
		}
		errDesc := "failed to transfer asset metadata"
		if len(failed) == 1 {
			errDesc = "failed to transfer " + failed[0]
		}
		return fmt.Errorf("%s; original asset preserved", errDesc)
	}

	return nil
}
